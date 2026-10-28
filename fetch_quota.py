#!/usr/bin/env python3
"""
LLM Quota / Limits Fetcher
Queries Antigravity (Gemini / Claude / GPT) via agy CLI (-p /usage --output-format json),
with Linux /proc Language Server fallback for WSL mode,
fetches OpenRouter and Kimi usages,
and atomically writes structured quota state (both remaining and consumed) to shared JSON files.
"""

import os
import sys
import glob
import json
import time
import shutil
import tempfile
import threading
import subprocess
import urllib.request
from datetime import datetime, timezone

HTTP_TIMEOUT_SECONDS = 3.5
# Таймаут на один запуск agy CLI: agy бывает зависшим (60+ с «context canceled»),
# а оверлей убивает fetch через 30 с — поэтому бюджет на agy должен быть заметно меньше.
AGY_TIMEOUT_SECONDS = 8
_MEMORY_CACHE = None

def get_shared_limits_paths():
    """Returns list of paths where limits.json should be written/read, fully dynamic without hardcoded paths."""
    paths = []
    # 1. Command-line arguments if provided
    for arg in sys.argv[1:]:
        if arg.endswith(".json") and not arg.startswith("--"):
            candidate = os.path.abspath(arg)
            if candidate not in paths:
                paths.append(candidate)
    # 2. Environment variable overrides
    for env_key in ["LLM_LIMITS_JSON", "AGY_LIMITS_JSON"]:
        val = os.environ.get(env_key, "").strip()
        if val and val not in paths:
            paths.append(os.path.abspath(val))
    # 3. Local limits.json next to this script
    script_dir = os.path.dirname(os.path.abspath(__file__))
    local_path = os.path.join(script_dir, "limits.json")
    if local_path not in paths:
        paths.append(local_path)
    # 4. Optional user home fallback (~/.gemini/antigravity-cli/limits.json) only if dir exists
    gemini_dir = os.path.expanduser("~/.gemini/antigravity-cli")
    if os.path.isdir(gemini_dir):
        gemini_path = os.path.join(gemini_dir, "limits.json")
        if gemini_path not in paths:
            paths.append(gemini_path)
    return paths

def get_env_file_path():
    """Returns single fixed path to .env file sitting next to this script."""
    return os.path.join(os.path.dirname(os.path.abspath(__file__)), ".env")

def parse_env_value(raw_val: str) -> str:
    """
    Parses a single environment variable value from .env.
    Tolerates single/double quotes around value and trailing inline comments outside quotes.
    """
    raw_val = raw_val.strip()
    if not raw_val:
        return ""
    quote_char = raw_val[0]
    if quote_char in ('"', "'"):
        val_chars = []
        i = 1
        n = len(raw_val)
        closed = False
        while i < n:
            ch = raw_val[i]
            if ch == '\\' and quote_char == '"' and i + 1 < n:
                next_ch = raw_val[i + 1]
                if next_ch in ('"', '\\', '$', '`'):
                    val_chars.append(next_ch)
                    i += 2
                    continue
                elif next_ch == 'n':
                    val_chars.append('\n')
                    i += 2
                    continue
                elif next_ch == 'r':
                    val_chars.append('\r')
                    i += 2
                    continue
                elif next_ch == 't':
                    val_chars.append('\t')
                    i += 2
                    continue
            if ch == quote_char:
                closed = True
                break
            val_chars.append(ch)
            i += 1
        if closed:
            return "".join(val_chars).strip()
        else:
            return raw_val[1:].strip()
    else:
        comment_idx = raw_val.find('#')
        if comment_idx != -1:
            raw_val = raw_val[:comment_idx]
        return raw_val.strip()

def parse_env_file(env_path):
    """
    Parses key-value pairs from .env using stdlib only.
    Tolerates UTF-8 BOM, CRLF, blank lines, comments (#), optional 'export ' prefix,
    'KEY = value' with spaces, single/double quotes around the value,
    and trailing inline comments outside quotes.
    Ignores malformed lines without raising.
    Strips whitespace. Never logs or emits key values.
    """
    entries = {}
    if not os.path.isfile(env_path):
        return entries
    try:
        with open(env_path, "r", encoding="utf-8-sig") as f:
            for line in f:
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                if line.startswith("export"):
                    rem = line[6:]
                    if rem and rem[0].isspace():
                        line = rem.lstrip()
                if "=" not in line:
                    continue
                parts = line.split("=", 1)
                key = parts[0].strip()
                raw_val = parts[1].strip()
                if not key:
                    continue
                val = parse_env_value(raw_val)
                entries[key] = val
    except Exception:
        pass
    return entries

def get_api_key_from_env(var_name):
    """
    Reads a single API key from the local .env file sitting next to this script.
    Never checks os.environ, registry, or any other directory.
    Returns: (key_value, error_message)
    Where key_value is a non-empty string or None.
    """
    env_path = get_env_file_path()
    if not os.path.isfile(env_path):
        return None, f"{var_name} не найдена в {env_path} (файла нет)"
    entries = parse_env_file(env_path)
    val = entries.get(var_name, "").strip()
    if not val:
        return None, f"{var_name} не найдена в {env_path} (файл есть)"
    return val, None

def format_time_diff(target_iso):
    if not target_iso:
        return "N/A"
    try:
        target = datetime.fromisoformat(target_iso.replace("Z", "+00:00"))
        now = datetime.now(timezone.utc)
        diff = target - now
        total_seconds = int(diff.total_seconds())
        if total_seconds <= 0:
            return "ready"
        days = total_seconds // 86400
        hours = (total_seconds % 86400) // 3600
        minutes = (total_seconds % 3600) // 60
        if days > 0:
            return f"{days}d {hours}h"
        if hours > 0:
            return f"{hours}h {minutes}m"
        return f"{minutes}m"
    except Exception:
        return target_iso

def get_used_status_level(used_fraction):
    if used_fraction is None:
        return "unknown"
    if used_fraction >= 0.50:
        return "critical"
    if used_fraction >= 0.20:
        return "warning"
    return "ok"

def get_openrouter_api_key():
    key, _ = get_api_key_from_env("OPENROUTER_API_KEY")
    return key

def query_openrouter_quota():
    key, err = get_api_key_from_env("OPENROUTER_API_KEY")
    if not key:
        return {
            "status": "disabled",
            "error": err,
            "limit": 0.0,
            "usage": 0.0,
            "limit_remaining": 0.0,
            "used_percent": 0.0,
            "used_fraction": 0.0,
            "reset_in": "--",
            "limit_reset": "monthly",
            "resets_at": "",
            "level": "unknown",
            "free_daily_used": 0,
            "free_daily_limit": 1000
        }

    url = "https://openrouter.ai/api/v1/key"
    req = urllib.request.Request(
        url,
        headers={
            "Authorization": f"Bearer {key}",
            "User-Agent": "LLM-Limits-Overlay/1.0"
        }
    )
    try:
        with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT_SECONDS) as resp:
            body = json.loads(resp.read().decode("utf-8"))
            data = body.get("data", {})

            limit = data.get("limit")
            usage = data.get("usage", 0.0)
            limit_remaining = data.get("limit_remaining", 0.0)
            limit_reset = data.get("limit_reset", "monthly")

            resets_at = ""
            budgets = data.get("budgets", [])
            if budgets and isinstance(budgets, list):
                resets_at = budgets[0].get("resets_at", "")
            if not resets_at and "effective_budget" in data:
                resets_at = data["effective_budget"].get("resets_at", "")

            reset_in = format_time_diff(resets_at) if resets_at else "--"

            used_frac = 0.0
            used_percent = 0.0
            limit_num = 0.0
            if limit is not None and float(limit) > 0:
                limit_num = float(limit)
                used_frac = max(0.0, min(1.0, float(usage) / limit_num))
                used_percent = round(used_frac * 100.0, 1)

            level = get_used_status_level(used_frac)

            free_daily = data.get("free_model_daily_requests", {})
            free_used = free_daily.get("used", 0)
            free_limit = free_daily.get("limit", 1000)

            return {
                "status": "online",
                "limit": limit_num,
                "usage": round(float(usage), 4),
                "limit_remaining": round(float(limit_remaining), 2),
                "used_fraction": round(used_frac, 4),
                "used_percent": used_percent,
                "limit_reset": limit_reset,
                "resets_at": resets_at,
                "reset_in": reset_in,
                "level": level,
                "free_daily_used": free_used,
                "free_daily_limit": free_limit,
                "label": data.get("label", "")
            }
    except Exception as e:
        return {
            "status": "error",
            "error": str(e),
            "limit": 0.0,
            "usage": 0.0,
            "limit_remaining": 0.0,
            "used_percent": 0.0,
            "used_fraction": 0.0,
            "reset_in": "--",
            "limit_reset": "monthly",
            "resets_at": "",
            "level": "unknown",
            "free_daily_used": 0,
            "free_daily_limit": 1000
        }

def get_kimi_api_key():
    key, _ = get_api_key_from_env("KIMI_API_KEY")
    return key

def query_kimi_quota():
    key, err = get_api_key_from_env("KIMI_API_KEY")
    default_res = {
        "status": "disabled",
        "error": err or "API key not found",
        "limit_5h": {
            "used_ratio": 0.0,
            "used_percent": 0.0,
            "reset_in": "--",
            "reset_time": "",
            "level": "unknown"
        },
        "limit_7d": {
            "used_ratio": 0.0,
            "used_percent": 0.0,
            "reset_in": "--",
            "reset_time": "",
            "level": "unknown"
        }
    }
    if not key:
        return default_res

    url = "https://api.kimi.com/coding/v1/usages"
    req = urllib.request.Request(
        url,
        headers={
            "Authorization": f"Bearer {key}",
            "User-Agent": "LLM-Limits-Overlay/1.0"
        }
    )
    try:
        with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT_SECONDS) as resp:
            data = json.loads(resp.read().decode("utf-8"))
            usages = data.get("usages", {})
            u5h = usages.get("limit_5h", {})
            u7d = usages.get("limit_7d", {})

            ratio_5h = u5h.get("used_ratio")
            time_5h = u5h.get("reset_time", "")
            if ratio_5h is None:
                limits = data.get("limits", [])
                for lim in limits:
                    w = lim.get("window", {})
                    if w.get("duration") == 300:
                        det = lim.get("detail", {})
                        l_val = float(det.get("limit", 100))
                        u_val = float(det.get("used", 0))
                        ratio_5h = (u_val / l_val) if l_val > 0 else 0.0
                        time_5h = det.get("resetTime", "")
                        break
            if ratio_5h is None:
                ratio_5h = 0.0

            ratio_7d = u7d.get("used_ratio")
            time_7d = u7d.get("reset_time", "")
            if ratio_7d is None:
                usage_obj = data.get("usage", {})
                l_val = float(usage_obj.get("limit", 100))
                u_val = float(usage_obj.get("used", 0))
                ratio_7d = (u_val / l_val) if l_val > 0 else 0.0
                time_7d = usage_obj.get("resetTime", "")
            if ratio_7d is None:
                ratio_7d = 0.0

            frac_5h = max(0.0, min(1.0, float(ratio_5h)))
            pct_5h = round(frac_5h * 100.0, 1)
            reset_in_5h = format_time_diff(time_5h) if time_5h else "--"

            frac_7d = max(0.0, min(1.0, float(ratio_7d)))
            pct_7d = round(frac_7d * 100.0, 1)
            reset_in_7d = format_time_diff(time_7d) if time_7d else "--"

            return {
                "status": "online",
                "limit_5h": {
                    "used_ratio": round(frac_5h, 4),
                    "used_percent": pct_5h,
                    "reset_in": reset_in_5h,
                    "reset_time": time_5h,
                    "level": get_used_status_level(frac_5h)
                },
                "limit_7d": {
                    "used_ratio": round(frac_7d, 4),
                    "used_percent": pct_7d,
                    "reset_in": reset_in_7d,
                    "reset_time": time_7d,
                    "level": get_used_status_level(frac_7d)
                }
            }
    except Exception as e:
        res = dict(default_res)
        res["status"] = "error"
        res["error"] = str(e)
        return res

# ------------------------------------------------------------------------------
# Antigravity (Gemini & Claude/GPT) Quota Discovery & Caching
# ------------------------------------------------------------------------------

def get_cache_file_path():
    """Returns path to cached agy /usage response file."""
    return os.path.join(tempfile.gettempdir(), "llm_limits_agy_usage_cache.json")

def get_quota_cache_seconds():
    """Returns cache duration in seconds (default: 300s)."""
    val = os.environ.get("LLM_LIMITS_QUOTA_CACHE_SECONDS", "").strip()
    if val:
        try:
            return max(0.0, float(val))
        except ValueError:
            pass
    return 300.0

def get_cached_agy_usage():
    """Retrieves cached agy usage response if valid and not expired."""
    global _MEMORY_CACHE
    cache_ttl = get_quota_cache_seconds()
    now = time.time()

    # 1. In-memory cache check
    if _MEMORY_CACHE and isinstance(_MEMORY_CACHE, dict):
        ts = _MEMORY_CACHE.get("timestamp", 0)
        age = now - ts
        if 0 <= age < cache_ttl:
            sys.stderr.write(f"[cache] agy /usage cache hit in-memory (age {age:.1f}s <= {cache_ttl:.0f}s), agy.exe not respawned\n")
            return _MEMORY_CACHE.get("raw"), _MEMORY_CACHE.get("data")

    # 2. Disk cache check
    cache_file = get_cache_file_path()
    if os.path.isfile(cache_file):
        try:
            with open(cache_file, "r", encoding="utf-8") as f:
                cached = json.load(f)
            ts = cached.get("timestamp", 0)
            age = now - ts
            if 0 <= age < cache_ttl and "raw" in cached and "data" in cached:
                _MEMORY_CACHE = cached
                sys.stderr.write(f"[cache] agy /usage cache hit (age {age:.1f}s <= {cache_ttl:.0f}s), agy.exe not respawned\n")
                return cached["raw"], cached["data"]
        except Exception:
            pass

    return None, None

def save_cached_agy_usage(raw_obj, data_obj):
    """Saves valid agy usage response in-memory and to disk cache without any secrets."""
    global _MEMORY_CACHE
    now = time.time()
    cache_record = {
        "timestamp": now,
        "raw": raw_obj,
        "data": data_obj
    }
    _MEMORY_CACHE = cache_record
    cache_file = get_cache_file_path()
    try:
        tmp_file = cache_file + ".tmp"
        with open(tmp_file, "w", encoding="utf-8") as f:
            json.dump(cache_record, f, ensure_ascii=False)
        os.replace(tmp_file, cache_file)
    except Exception:
        pass

def find_agy_binary():
    r"""
    Locates agy binary in order:
    1. LLM_LIMITS_AGY_BIN
    2. On Linux/WSL: native Linux agy executable (never .exe)
    3. On Windows: agy.exe / agy on PATH, %LOCALAPPDATA%\agy\bin\agy.exe, %USERPROFILE%\AppData\Local\agy\bin\agy.exe
    """
    # 1. env LLM_LIMITS_AGY_BIN
    env_bin = os.environ.get("LLM_LIMITS_AGY_BIN", "").strip()
    if env_bin and os.path.isfile(env_bin):
        if not sys.platform.startswith("linux") or not env_bin.endswith(".exe"):
            return env_bin

    # 2. Linux / WSL resolution: strictly avoid invoking Windows .exe to prevent
    # ConPTY / vsock hangs and unexpected OAuth browser launch on Windows host.
    if sys.platform.startswith("linux"):
        for cand in [
            os.path.expanduser("~/.local/bin/agy"),
            "/usr/local/bin/agy",
            "/usr/bin/agy"
        ]:
            if os.path.isfile(cand) and not cand.endswith(".exe"):
                return cand
        found = shutil.which("agy")
        if found and os.path.isfile(found) and not found.endswith(".exe"):
            return found
        return None

    # 3. Windows host resolution
    for name in ["agy.exe", "agy"]:
        found = shutil.which(name)
        if found and os.path.isfile(found):
            return found

    local_app_data = os.environ.get("LOCALAPPDATA", "")
    if local_app_data:
        cand = os.path.join(local_app_data, "agy", "bin", "agy.exe")
        if os.path.isfile(cand):
            return cand

    user_prof = os.environ.get("USERPROFILE", "")
    if user_prof:
        cand = os.path.join(user_prof, "AppData", "Local", "agy", "bin", "agy.exe")
        if os.path.isfile(cand):
            return cand

    home = os.path.expanduser("~")
    cand = os.path.join(home, "AppData", "Local", "agy", "bin", "agy.exe")
    if os.path.isfile(cand):
        return cand

    return None

def extract_last_json(text):
    """Tolerate CRLF and leading noise before the JSON line (parse the last JSON object in stdout)."""
    if not text:
        return None
    lines = text.strip().splitlines()
    for line in reversed(lines):
        line = line.strip()
        if line.startswith("{") and line.endswith("}"):
            try:
                return json.loads(line)
            except Exception:
                pass

    # Fallback: find outermost JSON brackets from the end
    start = text.rfind("{")
    end = text.rfind("}")
    if start != -1 and end != -1 and end > start:
        try:
            return json.loads(text[start:end+1])
        except Exception:
            pass
    return None

def query_live_quota_proc():
    """Fallback Language Server discovery via Linux /proc/[0-9]*/environ for WSL mode."""
    checked_tokens = set()
    for p in glob.glob('/proc/[0-9]*/environ'):
        try:
            with open(p, 'rb') as f:
                content = f.read()
            if b'ANTIGRAVITY_CSRF_TOKEN' not in content or b'ANTIGRAVITY_LS_ADDRESS' not in content:
                continue
            env = dict(item.split(b'=', 1) for item in content.split(b'\x00') if b'=' in item)
            token = env.get(b'ANTIGRAVITY_CSRF_TOKEN', b'').decode('utf-8', errors='ignore')
            addr = env.get(b'ANTIGRAVITY_LS_ADDRESS', b'').decode('utf-8', errors='ignore')
            if not token or not addr or token in checked_tokens:
                continue
            checked_tokens.add(token)

            url = f"http://{addr}/exa.language_server_pb.LanguageServerService/RetrieveUserQuotaSummary"
            req = urllib.request.Request(
                url,
                data=b'{}',
                headers={
                    "Content-Type": "application/json",
                    "x-codeium-csrf-token": token
                }
            )
            with urllib.request.urlopen(req, timeout=HTTP_TIMEOUT_SECONDS) as resp:
                data = json.loads(resp.read().decode('utf-8'))
                if "response" in data and "groups" in data["response"]:
                    return data["response"], addr
        except Exception:
            continue
    return None, None

def fetch_agy_quota():
    """
    Executes agy -p /usage --output-format json with 60s timeout,
    CREATE_NO_WINDOW, no console flash, captures stdout.
    Caches result for 300s.
    Returns: (command_data, raw_envelope, error_message, is_cached)
    """
    # 1. Cache lookup
    cached_raw, cached_data = get_cached_agy_usage()
    if cached_raw is not None and cached_data is not None:
        return cached_data, cached_raw, None, True

    # 2. Locate agy binary
    agy_bin = find_agy_binary()
    if not agy_bin:
        # Fallback to /proc if on Linux / WSL
        if sys.platform.startswith("linux") or os.path.isdir("/proc"):
            proc_resp, proc_addr = query_live_quota_proc()
            if proc_resp:
                save_cached_agy_usage(proc_resp, proc_resp)
                return proc_resp, proc_resp, None, False
        return None, None, "missing agy (not found in LLM_LIMITS_AGY_BIN, PATH, or AppData)", False

    # 3. Prevent agy from triggering interactive OAuth browser popups if not authenticated
    if sys.platform.startswith("win"):
        tok_path = os.path.expanduser("~/.gemini/antigravity-cli/antigravity-oauth-token")
        if not os.path.isfile(tok_path) or os.path.getsize(tok_path) == 0:
            return None, None, "auth required: agy not logged in (run 'agy auth login')", False

    # 4. Execute subprocess
    sys.stderr.write(f"[live] running {agy_bin} -p /usage --output-format json...\n")
    cmd = [agy_bin, "-p", "/usage", "--output-format", "json"]

    creationflags = 0
    if sys.platform.startswith("win"):
        creationflags = getattr(subprocess, "CREATE_NO_WINDOW", 0x08000000)

    try:
        proc = subprocess.run(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            timeout=AGY_TIMEOUT_SECONDS,
            creationflags=creationflags
        )
    except subprocess.TimeoutExpired:
        if sys.platform.startswith("linux") or os.path.isdir("/proc"):
            proc_resp, proc_addr = query_live_quota_proc()
            if proc_resp:
                save_cached_agy_usage(proc_resp, proc_resp)
                return proc_resp, proc_resp, None, False
        return None, None, "timeout after 60s executing agy", False
    except Exception as e:
        if sys.platform.startswith("linux") or os.path.isdir("/proc"):
            proc_resp, proc_addr = query_live_quota_proc()
            if proc_resp:
                save_cached_agy_usage(proc_resp, proc_resp)
                return proc_resp, proc_resp, None, False
        return None, None, f"failed to spawn agy: {e}", False

    stdout = proc.stdout or ""
    stderr = proc.stderr or ""

    if proc.returncode != 0:
        combined = (stdout + " " + stderr).lower()
        if "auth" in combined or "login" in combined or "unauthorized" in combined:
            err = "auth required"
        else:
            err = f"agy failed with exit code {proc.returncode}: {stderr.strip() or stdout.strip()}"

        if sys.platform.startswith("linux") or os.path.isdir("/proc"):
            proc_resp, proc_addr = query_live_quota_proc()
            if proc_resp:
                save_cached_agy_usage(proc_resp, proc_resp)
                return proc_resp, proc_resp, None, False
        return None, None, err, False

    parsed = extract_last_json(stdout)
    if not parsed or not isinstance(parsed, dict):
        if sys.platform.startswith("linux") or os.path.isdir("/proc"):
            proc_resp, proc_addr = query_live_quota_proc()
            if proc_resp:
                save_cached_agy_usage(proc_resp, proc_resp)
                return proc_resp, proc_resp, None, False
        return None, None, "parse failed: invalid JSON output from agy", False

    status_str = str(parsed.get("status", "")).upper()
    if status_str in ["ERROR", "FAILED"]:
        resp_text = parsed.get("response", "")
        if "auth" in resp_text.lower() or "login" in resp_text.lower():
            err = "auth required"
        else:
            err = f"agy error: {resp_text or 'unknown'}"
        return None, None, err, False

    # Extract inner command data
    cmd_data = parsed.get("command", {}).get("data")
    if not cmd_data:
        cmd_data = parsed.get("response") or parsed

    save_cached_agy_usage(parsed, cmd_data)
    return cmd_data, parsed, None, False

def parse_quota_data(resp):
    default_bucket = {
        "percent": 100.0,
        "fraction": 1.0,
        "used_percent": 0.0,
        "used_fraction": 0.0,
        "reset_in": "N/A",
        "reset_time": "",
        "level": "ok"
    }
    result = {
        "gemini_5h": dict(default_bucket),
        "gemini_weekly": dict(default_bucket),
        "claude_5h": dict(default_bucket),
        "claude_weekly": dict(default_bucket),
    }

    if not resp or not isinstance(resp, dict):
        return result

    if "command" in resp and isinstance(resp["command"], dict):
        data_obj = resp["command"].get("data", {})
    elif "response" in resp and isinstance(resp["response"], dict):
        data_obj = resp["response"]
    else:
        data_obj = resp

    groups = data_obj.get("groups", [])
    for group in groups:
        gname = (group.get("name") or group.get("displayName") or "").lower()
        for b in group.get("buckets", []):
            bid = (b.get("id") or b.get("bucketId") or "").lower()
            window = (b.get("window") or "").lower()

            if "remaining_fraction" in b:
                frac = b.get("remaining_fraction")
            elif "remainingFraction" in b:
                frac = b.get("remainingFraction")
            else:
                frac = 1.0

            if frac is None:
                frac = 1.0
            try:
                frac = float(frac)
            except (ValueError, TypeError):
                frac = 1.0

            reset_time = b.get("reset_time") or b.get("resetTime") or ""
            percent = round(frac * 100, 1)

            # Consumed / Used quota
            used_frac = max(0.0, min(1.0, round(1.0 - frac, 4)))
            used_percent = round(used_frac * 100, 1)
            level = get_used_status_level(used_frac)
            reset_in = format_time_diff(reset_time)

            info = {
                "percent": percent,
                "fraction": frac,
                "used_percent": used_percent,
                "used_fraction": used_frac,
                "reset_in": reset_in,
                "reset_time": reset_time,
                "level": level,
                "displayName": b.get("name") or b.get("displayName", bid),
                "description": b.get("description", "")
            }

            # Map by window value plus group name (NOT by bucket id alone; ids differ: gemini-* vs 3p-*)
            if "gemini" in gname:
                if window == "5h" or "5h" in bid:
                    result["gemini_5h"] = info
                elif window == "weekly" or "weekly" in bid:
                    result["gemini_weekly"] = info
            elif "claude" in gname or "gpt" in gname or "3p" in gname:
                if window == "5h" or "5h" in bid:
                    result["claude_5h"] = info
                elif window == "weekly" or "weekly" in bid:
                    result["claude_weekly"] = info
            else:
                if "gemini" in bid:
                    if window == "5h" or "5h" in bid:
                        result["gemini_5h"] = info
                    elif window == "weekly" or "weekly" in bid:
                        result["gemini_weekly"] = info
                elif "3p" in bid or "claude" in bid or "gpt" in bid:
                    if window == "5h" or "5h" in bid:
                        result["claude_5h"] = info
                    elif window == "weekly" or "weekly" in bid:
                        result["claude_weekly"] = info

    return result

# ------------------------------------------------------------------------------
# Per-Provider Fragments & Independent Concurrent Fetch
# ------------------------------------------------------------------------------

def get_fragment_path(limits_path, provider):
    """
    Derives fragment path next to the target limits.json.
    e.g. /path/to/limits.json -> /path/to/limits.kimi.json
    """
    dirname = os.path.dirname(os.path.abspath(limits_path))
    basename = os.path.basename(limits_path)
    if basename.endswith(".json"):
        stem = basename[:-5]
        frag_name = f"{stem}.{provider}.json"
    else:
        frag_name = f"{basename}.{provider}.json"
    return os.path.join(dirname, frag_name)

def write_atomic_json(target_path, data_obj):
    """Atomically writes JSON object to target_path using a temporary file and replace."""
    try:
        target_abs = os.path.abspath(target_path)
        os.makedirs(os.path.dirname(target_abs), exist_ok=True)
        tmp_path = target_abs + ".tmp"
        json_str = json.dumps(data_obj, indent=2, ensure_ascii=False)
        with open(tmp_path, "w", encoding="utf-8") as f:
            f.write(json_str)
        os.replace(tmp_path, target_abs)
        return True
    except Exception as e:
        sys.stderr.write(f"[write_atomic_json] Error writing {target_path}: {e}\n")
        return False

def save_provider_fragment(provider, fragment_dict, limits_paths):
    """Saves a single provider's fragment atomically to all target paths."""
    for path in limits_paths:
        frag_path = get_fragment_path(path, provider)
        write_atomic_json(frag_path, fragment_dict)

def extract_provider_from_payload(provider, payload):
    """Extracts provider-specific dictionary from a fragment or root limits.json."""
    if not isinstance(payload, dict):
        return None

    if provider == "kimi":
        data_block = payload.get("data")
        if isinstance(data_block, dict) and "kimi" in data_block:
            return data_block["kimi"]
        if isinstance(data_block, dict) and "limit_5h" in data_block:
            return data_block
        if "kimi" in payload and isinstance(payload["kimi"], dict):
            return payload["kimi"]
        if "limit_5h" in payload:
            return {k: v for k, v in payload.items() if k not in ("data", "last_updated") or k == "status"}
        return None

    elif provider == "openrouter":
        data_block = payload.get("data")
        if isinstance(data_block, dict) and "openrouter" in data_block:
            return data_block["openrouter"]
        if isinstance(data_block, dict) and ("limit" in data_block or "usage" in data_block):
            return data_block
        if "openrouter" in payload and isinstance(payload["openrouter"], dict):
            return payload["openrouter"]
        if "limit" in payload or "usage" in payload:
            return {k: v for k, v in payload.items() if k not in ("data", "last_updated") or k == "status"}
        return None

    elif provider == "agy":
        data_block = payload.get("data")
        if isinstance(data_block, dict) and "gemini_5h" in data_block:
            buckets = {k: v for k, v in data_block.items() if k in ("gemini_5h", "gemini_weekly", "claude_5h", "claude_weekly")}
            return {
                "buckets": buckets,
                "raw": payload.get("raw"),
                "status": payload.get("status", "online")
            }
        if "gemini_5h" in payload:
            buckets = {k: v for k, v in payload.items() if k in ("gemini_5h", "gemini_weekly", "claude_5h", "claude_weekly")}
            return {
                "buckets": buckets,
                "raw": payload.get("raw"),
                "status": payload.get("status", "online")
            }
        return None

    return None

def load_cached_provider(provider, limits_paths):
    """
    Attempts to load cached provider data from limits.<provider>.json fragment,
    falling back to existing limits.json.
    """
    # 1. Check per-provider fragment
    for path in limits_paths:
        frag_path = get_fragment_path(path, provider)
        if os.path.isfile(frag_path):
            try:
                with open(frag_path, "r", encoding="utf-8") as f:
                    frag = json.load(f)
                if isinstance(frag, dict):
                    extracted = extract_provider_from_payload(provider, frag)
                    if extracted:
                        return extracted
            except Exception:
                pass

    # 2. Check unified limits.json
    for path in limits_paths:
        if os.path.isfile(path):
            try:
                with open(path, "r", encoding="utf-8") as f:
                    root = json.load(f)
                if isinstance(root, dict):
                    extracted = extract_provider_from_payload(provider, root)
                    if extracted:
                        return extracted
            except Exception:
                pass

    return None

def get_default_agy_buckets(level="unknown"):
    default_item = {
        "percent": 100.0,
        "fraction": 1.0,
        "used_percent": 0.0,
        "used_fraction": 0.0,
        "reset_in": "N/A",
        "reset_time": "",
        "level": level
    }
    return {
        "gemini_5h": dict(default_item),
        "gemini_weekly": dict(default_item),
        "claude_5h": dict(default_item),
        "claude_weekly": dict(default_item),
    }

def get_default_openrouter(status="disabled", error=None):
    return {
        "status": status,
        "error": error or "OpenRouter not configured",
        "limit": 0.0,
        "usage": 0.0,
        "limit_remaining": 0.0,
        "used_percent": 0.0,
        "used_fraction": 0.0,
        "reset_in": "--",
        "limit_reset": "monthly",
        "resets_at": "",
        "level": "unknown",
        "free_daily_used": 0,
        "free_daily_limit": 1000
    }

def get_default_kimi(status="disabled", error=None):
    return {
        "status": status,
        "error": error or "Kimi not configured",
        "limit_5h": {
            "used_ratio": 0.0,
            "used_percent": 0.0,
            "reset_in": "--",
            "reset_time": "",
            "level": "unknown"
        },
        "limit_7d": {
            "used_ratio": 0.0,
            "used_percent": 0.0,
            "reset_in": "--",
            "reset_time": "",
            "level": "unknown"
        }
    }

def fetch_worker_agy(simulate_offline, limits_paths, now_iso):
    """Fetches Antigravity (Gemini/Claude) quota, saves fragment, returns result dict."""
    if simulate_offline:
        data, raw, err_msg, is_cached = None, None, "offline (simulated)", False
    else:
        data, raw, err_msg, is_cached = fetch_agy_quota()

    if data:
        parsed = parse_quota_data(data or raw)
        agy_status = "online"
        raw_payload = raw
    else:
        prev = load_cached_provider("agy", limits_paths)
        if prev and isinstance(prev, dict) and "buckets" in prev:
            parsed = {k: dict(v) for k, v in prev["buckets"].items()}
            raw_payload = prev.get("raw")
            agy_status = "stale"
        else:
            parsed = get_default_agy_buckets(level="unknown")
            raw_payload = None
            agy_status = "error"

    fragment = {
        "status": agy_status,
        "last_updated": now_iso,
        "data": parsed,
        "raw": raw_payload,
        **parsed
    }
    if is_cached:
        fragment["cache_hit"] = True
    if err_msg:
        fragment["error"] = err_msg

    save_provider_fragment("agy", fragment, limits_paths)
    return {
        "status": agy_status,
        "buckets": parsed,
        "raw": raw_payload,
        "cache_hit": is_cached,
        "error": err_msg
    }

def fetch_worker_openrouter(mock_offline, limits_paths, now_iso):
    """Fetches OpenRouter quota, saves fragment, returns result dict."""
    if mock_offline:
        or_data = get_default_openrouter(status="error", error="OpenRouter offline (mocked)")
    else:
        or_data = query_openrouter_quota()

    if or_data.get("status") == "error":
        prev = load_cached_provider("openrouter", limits_paths)
        if prev and isinstance(prev, dict) and prev.get("status") in ("online", "stale"):
            prev_stale = dict(prev)
            prev_stale["status"] = "stale"
            or_data = prev_stale

    or_status = or_data.get("status", "unknown")
    fragment = {
        "status": or_status,
        "last_updated": now_iso,
        "data": or_data,
        **or_data
    }

    save_provider_fragment("openrouter", fragment, limits_paths)
    return or_data

def fetch_worker_kimi(mock_offline, limits_paths, now_iso):
    """Fetches Kimi quota, saves fragment, returns result dict."""
    if mock_offline:
        kimi_data = get_default_kimi(status="error", error="Kimi offline (mocked)")
    else:
        kimi_data = query_kimi_quota()

    if kimi_data.get("status") == "error":
        prev = load_cached_provider("kimi", limits_paths)
        if prev and isinstance(prev, dict) and prev.get("status") in ("online", "stale"):
            prev_stale = dict(prev)
            prev_stale["status"] = "stale"
            kimi_data = prev_stale

    kimi_status = kimi_data.get("status", "unknown")
    fragment = {
        "status": kimi_status,
        "last_updated": now_iso,
        "data": kimi_data,
        **kimi_data
    }

    save_provider_fragment("kimi", fragment, limits_paths)
    return kimi_data

def assemble_and_save_limits(limits_paths, now_iso, agy_res, or_res, kimi_res):
    """
    Merges provider fragments/results into the unified limits.json.
    Root status: 'online' if at least one provider is online; 'error' only if all failed.
    """
    agy_buckets = agy_res.get("buckets", get_default_agy_buckets())
    agy_raw = agy_res.get("raw")
    agy_status = agy_res.get("status", "unknown")
    agy_cache_hit = agy_res.get("cache_hit", False)

    or_data = or_res
    or_status = or_data.get("status", "unknown")

    kimi_data = kimi_res
    kimi_status = kimi_data.get("status", "unknown")

    # Compute root status
    statuses = [agy_status, or_status, kimi_status]
    if "online" in statuses:
        root_status = "online"
    elif all(s == "error" for s in statuses):
        root_status = "error"
    elif "stale" in statuses:
        root_status = "stale"
    else:
        root_status = "error" if all(s in ("error", None) for s in statuses) else "online"

    merged_data = {
        "gemini_5h": agy_buckets.get("gemini_5h"),
        "gemini_weekly": agy_buckets.get("gemini_weekly"),
        "claude_5h": agy_buckets.get("claude_5h"),
        "claude_weekly": agy_buckets.get("claude_weekly"),
        "openrouter": or_data,
        "kimi": kimi_data
    }

    payload = {
        "status": root_status,
        "last_updated": now_iso,
        "ls_address": None,
        "data": merged_data,
        "raw": agy_raw,
        "openrouter": or_data,
        "kimi": kimi_data
    }
    if agy_cache_hit:
        payload["cache_hit"] = True

    json_str = json.dumps(payload, indent=2, ensure_ascii=False)
    for path in limits_paths:
        write_atomic_json(path, payload)

    print(json_str)
    return payload

def main():
    mock_offline_providers = set()
    simulate_delay = 0
    target_provider = None

    i = 1
    while i < len(sys.argv):
        arg = sys.argv[i]
        if arg == "--mock-offline":
            mock_offline_providers.add("agy")
        elif arg.startswith("--mock-offline="):
            val = arg.split("=", 1)[1].strip().lower()
            if val in ("all", "1", "true"):
                mock_offline_providers.update(["agy", "openrouter", "kimi"])
            else:
                for p in val.split(","):
                    mock_offline_providers.add(p.strip().lower())
        elif arg.startswith("--delay="):
            try:
                simulate_delay = float(arg.split("=", 1)[1])
            except ValueError:
                pass
        elif arg.startswith("--provider="):
            target_provider = arg.split("=", 1)[1].strip().lower()
        elif arg == "--provider" and i + 1 < len(sys.argv):
            i += 1
            target_provider = sys.argv[i].strip().lower()
        i += 1

    if target_provider and target_provider not in ("agy", "openrouter", "kimi"):
        sys.stderr.write(f"[fetch_quota] Error: unknown provider '{target_provider}'. Valid options: agy, openrouter, kimi\n")
        sys.exit(1)

    if simulate_delay > 0:
        time.sleep(simulate_delay)

    now_iso = datetime.now().strftime("%Y-%m-%d %H:%M:%S")
    limits_paths = get_shared_limits_paths()

    # Branch 1: Single provider update (--provider <name>)
    if target_provider == "agy":
        agy_res = fetch_worker_agy("agy" in mock_offline_providers, limits_paths, now_iso)

        prev_or = load_cached_provider("openrouter", limits_paths)
        or_res = prev_or if prev_or else get_default_openrouter()

        prev_kimi = load_cached_provider("kimi", limits_paths)
        kimi_res = prev_kimi if prev_kimi else get_default_kimi()

        assemble_and_save_limits(limits_paths, now_iso, agy_res, or_res, kimi_res)
        return

    elif target_provider == "openrouter":
        or_res = fetch_worker_openrouter("openrouter" in mock_offline_providers, limits_paths, now_iso)

        prev_agy = load_cached_provider("agy", limits_paths)
        if prev_agy and isinstance(prev_agy, dict) and "buckets" in prev_agy:
            agy_res = {
                "status": prev_agy.get("status", "online"),
                "buckets": prev_agy["buckets"],
                "raw": prev_agy.get("raw"),
                "cache_hit": False
            }
        else:
            agy_res = {
                "status": "unknown",
                "buckets": get_default_agy_buckets(),
                "raw": None,
                "cache_hit": False
            }

        prev_kimi = load_cached_provider("kimi", limits_paths)
        kimi_res = prev_kimi if prev_kimi else get_default_kimi()

        assemble_and_save_limits(limits_paths, now_iso, agy_res, or_res, kimi_res)
        return

    elif target_provider == "kimi":
        kimi_res = fetch_worker_kimi("kimi" in mock_offline_providers, limits_paths, now_iso)

        prev_agy = load_cached_provider("agy", limits_paths)
        if prev_agy and isinstance(prev_agy, dict) and "buckets" in prev_agy:
            agy_res = {
                "status": prev_agy.get("status", "online"),
                "buckets": prev_agy["buckets"],
                "raw": prev_agy.get("raw"),
                "cache_hit": False
            }
        else:
            agy_res = {
                "status": "unknown",
                "buckets": get_default_agy_buckets(),
                "raw": None,
                "cache_hit": False
            }

        prev_or = load_cached_provider("openrouter", limits_paths)
        or_res = prev_or if prev_or else get_default_openrouter()

        assemble_and_save_limits(limits_paths, now_iso, agy_res, or_res, kimi_res)
        return

    # Branch 2: Parallel fetch for all 3 providers
    results = {}

    def run_agy():
        try:
            results["agy"] = fetch_worker_agy("agy" in mock_offline_providers, limits_paths, now_iso)
        except Exception as e:
            sys.stderr.write(f"[agy-worker] Uncaught error: {e}\n")

    def run_or():
        try:
            results["openrouter"] = fetch_worker_openrouter("openrouter" in mock_offline_providers, limits_paths, now_iso)
        except Exception as e:
            sys.stderr.write(f"[or-worker] Uncaught error: {e}\n")

    def run_kimi():
        try:
            results["kimi"] = fetch_worker_kimi("kimi" in mock_offline_providers, limits_paths, now_iso)
        except Exception as e:
            sys.stderr.write(f"[kimi-worker] Uncaught error: {e}\n")

    threads = [
        threading.Thread(target=run_agy, name="worker-agy", daemon=True),
        threading.Thread(target=run_or, name="worker-or", daemon=True),
        threading.Thread(target=run_kimi, name="worker-kimi", daemon=True)
    ]

    for t in threads:
        t.start()

    # Overall join deadline < 20s (budget: worst case < 20s)
    join_deadline = 18.0
    start_join = time.time()
    for t in threads:
        remaining = max(0.1, join_deadline - (time.time() - start_join))
        t.join(timeout=remaining)

    # Check any worker that didn't finish (timeout or unhandled error)
    if "agy" not in results:
        sys.stderr.write("[main] Warning: agy worker timed out or failed\n")
        prev_agy = load_cached_provider("agy", limits_paths)
        if prev_agy and isinstance(prev_agy, dict) and "buckets" in prev_agy:
            agy_res = {
                "status": "stale",
                "buckets": prev_agy["buckets"],
                "raw": prev_agy.get("raw"),
                "cache_hit": False
            }
        else:
            agy_res = {
                "status": "error",
                "buckets": get_default_agy_buckets(),
                "raw": None,
                "cache_hit": False
            }
    else:
        agy_res = results["agy"]

    if "openrouter" not in results:
        sys.stderr.write("[main] Warning: openrouter worker timed out or failed\n")
        prev_or = load_cached_provider("openrouter", limits_paths)
        if prev_or and isinstance(prev_or, dict):
            or_res = dict(prev_or)
            or_res["status"] = "stale"
        else:
            or_res = get_default_openrouter(status="error", error="timeout")
    else:
        or_res = results["openrouter"]

    if "kimi" not in results:
        sys.stderr.write("[main] Warning: kimi worker timed out or failed\n")
        prev_kimi = load_cached_provider("kimi", limits_paths)
        if prev_kimi and isinstance(prev_kimi, dict):
            kimi_res = dict(prev_kimi)
            kimi_res["status"] = "stale"
        else:
            kimi_res = get_default_kimi(status="error", error="timeout")
    else:
        kimi_res = results["kimi"]

    assemble_and_save_limits(limits_paths, now_iso, agy_res, or_res, kimi_res)

if __name__ == "__main__":
    main()
