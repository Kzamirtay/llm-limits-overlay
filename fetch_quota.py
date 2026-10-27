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
import subprocess
import urllib.request
from datetime import datetime, timezone

HTTP_TIMEOUT_SECONDS = 3.5
_MEMORY_CACHE = None

def get_shared_limits_paths():
    """Returns list of paths where limits.json should be written/read, fully dynamic without hardcoded paths."""
    paths = []
    # 1. Command-line argument if provided
    if len(sys.argv) > 1 and sys.argv[1].endswith(".json"):
        paths.append(os.path.abspath(sys.argv[1]))
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
            timeout=60,
            creationflags=creationflags
        )
    except subprocess.TimeoutExpired:
        if sys.platform.startswith("linux") or os.path.isdir("/proc"):
            proc_resp, proc_addr = query_live_quota_proc()
            if proc_resp:
                return proc_resp, proc_resp, None, False
        return None, None, "timeout after 60s executing agy", False
    except Exception as e:
        if sys.platform.startswith("linux") or os.path.isdir("/proc"):
            proc_resp, proc_addr = query_live_quota_proc()
            if proc_resp:
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
                return proc_resp, proc_resp, None, False
        return None, None, err, False

    parsed = extract_last_json(stdout)
    if not parsed or not isinstance(parsed, dict):
        if sys.platform.startswith("linux") or os.path.isdir("/proc"):
            proc_resp, proc_addr = query_live_quota_proc()
            if proc_resp:
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

def main():
    simulate_offline = "--mock-offline" in sys.argv
    simulate_delay = 0
    for arg in sys.argv:
        if arg.startswith("--delay="):
            simulate_delay = float(arg.split("=")[1])

    if simulate_delay > 0:
        time.sleep(simulate_delay)

    now_iso = datetime.now().strftime("%Y-%m-%d %H:%M:%S")

    # Fallback cache loader helper
    last_cache = None
    for path in get_shared_limits_paths():
        if os.path.exists(path):
            try:
                with open(path, 'r', encoding='utf-8') as f:
                    last_cache = json.load(f)
                break
            except Exception:
                pass

    if simulate_offline:
        data, raw, err_msg, is_cached = None, None, "offline (simulated)", False
    else:
        data, raw, err_msg, is_cached = fetch_agy_quota()

    if data:
        parsed = parse_quota_data(data or raw)
        payload = {
            "status": "online",
            "last_updated": now_iso,
            "ls_address": None,
            "data": parsed,
            "raw": raw
        }
        if is_cached:
            payload["cache_hit"] = True
    else:
        if last_cache and "data" in last_cache:
            data_buckets = {
                k: v for k, v in last_cache["data"].items()
                if k in ["gemini_5h", "gemini_weekly", "claude_5h", "claude_weekly"]
            }
        else:
            default_item = {
                "percent": 100.0,
                "fraction": 1.0,
                "used_percent": 0.0,
                "used_fraction": 0.0,
                "reset_in": "N/A",
                "reset_time": "",
                "level": "unknown"
            }
            data_buckets = {
                "gemini_5h": dict(default_item),
                "gemini_weekly": dict(default_item),
                "claude_5h": dict(default_item),
                "claude_weekly": dict(default_item),
            }

        payload = {
            "status": "error",
            "error": err_msg or "Failed to query quota",
            "last_updated": now_iso,
            "ls_address": None,
            "data": data_buckets,
            "raw": None
        }

    # Query OpenRouter quota and attach
    openrouter_data = query_openrouter_quota()
    if openrouter_data.get("status") == "error" and last_cache:
        cached_or = last_cache.get("data", {}).get("openrouter") or last_cache.get("openrouter")
        if cached_or and cached_or.get("status") in ("online", "stale"):
            cached_or = dict(cached_or)
            cached_or["status"] = "stale"
            openrouter_data = cached_or

    payload["data"]["openrouter"] = openrouter_data
    payload["openrouter"] = openrouter_data

    # Query Kimi quota and attach
    kimi_data = query_kimi_quota()
    if kimi_data.get("status") == "error" and last_cache:
        cached_kimi = last_cache.get("data", {}).get("kimi") or last_cache.get("kimi")
        if cached_kimi and cached_kimi.get("status") in ("online", "stale"):
            cached_kimi = dict(cached_kimi)
            cached_kimi["status"] = "stale"
            kimi_data = cached_kimi

    payload["data"]["kimi"] = kimi_data
    payload["kimi"] = kimi_data

    # Atomic write to shared destinations
    json_str = json.dumps(payload, indent=2, ensure_ascii=False)
    for path in get_shared_limits_paths():
        try:
            os.makedirs(os.path.dirname(path), exist_ok=True)
            tmp_path = path + ".tmp"
            with open(tmp_path, 'w', encoding='utf-8') as f:
                f.write(json_str)
            os.replace(tmp_path, path)
        except Exception:
            pass

    print(json_str)

if __name__ == "__main__":
    main()
