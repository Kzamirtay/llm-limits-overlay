#!/usr/bin/env python3
"""
LLM Quota / Limits Fetcher
Discovers active language server instances from /proc,
queries RetrieveUserQuotaSummary via Connect RPC (Gemini / Claude),
fetches OpenRouter and Kimi usages,
and atomically writes structured quota state (both remaining and consumed) to shared JSON files.
"""

import os
import sys
import glob
import json
import time
import urllib.request
from datetime import datetime, timezone

HTTP_TIMEOUT_SECONDS = 3.5

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

def get_env_file_candidates():
    """Returns list of candidate .env files to check for API keys."""
    paths = []
    custom = os.environ.get("HERMES_ENV_PATH", "").strip()
    if custom:
        paths.append(os.path.expanduser(custom))
    paths.append(os.path.expanduser("~/.hermes/.env"))
    script_dir = os.path.dirname(os.path.abspath(__file__))
    paths.append(os.path.join(script_dir, ".env"))
    return [p for p in paths if os.path.isfile(p)]

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
    key = os.environ.get("OPENROUTER_API_KEY", "").strip()
    if key:
        return key
    for env_path in get_env_file_candidates():
        try:
            with open(env_path, "r", encoding="utf-8") as f:
                for line in f:
                    line = line.strip()
                    if line.startswith("#") or not line:
                        continue
                    if line.startswith("OPENROUTER_API_KEY="):
                        val = line.split("=", 1)[1].strip().strip("\"'")
                        if val:
                            return val
        except Exception:
            pass
    return None

def query_openrouter_quota():
    key = get_openrouter_api_key()
    if not key:
        return {
            "status": "disabled",
            "error": "No API key found in env or ~/.hermes/.env",
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
    key = os.environ.get("KIMI_API_KEY", "").strip()
    if key:
        return key
    for env_path in get_env_file_candidates():
        try:
            with open(env_path, "r", encoding="utf-8") as f:
                for line in f:
                    line = line.strip()
                    if line.startswith("#") or not line:
                        continue
                    if line.startswith("KIMI_API_KEY="):
                        val = line.split("=", 1)[1].strip().strip("\"'")
                        if val:
                            return val
        except Exception:
            pass
    return None

def query_kimi_quota():
    key = get_kimi_api_key()
    default_res = {
        "status": "disabled",
        "error": "No API key found in env or ~/.hermes/.env",
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

def query_live_quota():
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

    groups = resp.get("groups", [])
    for group in groups:
        gname = group.get("displayName", "").lower()
        for b in group.get("buckets", []):
            bid = b.get("bucketId", "").lower()
            window = b.get("window", "").lower()
            frac = b.get("remainingFraction", 1.0)
            reset_time = b.get("resetTime", "")
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
                "displayName": b.get("displayName", bid),
                "description": b.get("description", "")
            }

            if "gemini" in gname or "gemini" in bid:
                if window == "5h" or "5h" in bid:
                    result["gemini_5h"] = info
                elif window == "weekly" or "weekly" in bid:
                    result["gemini_weekly"] = info
            elif "claude" in gname or "3p" in bid or "gpt" in gname:
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

    if simulate_offline:
        resp, addr = None, None
    else:
        resp, addr = query_live_quota()

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

    if resp:
        parsed = parse_quota_data(resp)
        payload = {
            "status": "online",
            "last_updated": now_iso,
            "ls_address": addr,
            "data": parsed,
            "raw": resp
        }
    else:
        if last_cache and "data" in last_cache:
            payload = last_cache
            payload["status"] = "stale (agy idle)"
        else:
            default_item = {
                "percent": 100.0,
                "fraction": 1.0,
                "used_percent": 0.0,
                "used_fraction": 0.0,
                "reset_in": "N/A",
                "level": "unknown"
            }
            payload = {
                "status": "offline",
                "last_updated": now_iso,
                "ls_address": None,
                "data": {
                    "gemini_5h": dict(default_item),
                    "gemini_weekly": dict(default_item),
                    "claude_5h": dict(default_item),
                    "claude_weekly": dict(default_item),
                }
            }

    # Query OpenRouter quota and attach
    openrouter_data = query_openrouter_quota()
    if openrouter_data.get("status") != "online" and last_cache:
        cached_or = last_cache.get("data", {}).get("openrouter") or last_cache.get("openrouter")
        if cached_or and cached_or.get("status") == "online":
            cached_or = dict(cached_or)
            cached_or["status"] = "stale"
            openrouter_data = cached_or

    payload["data"]["openrouter"] = openrouter_data
    payload["openrouter"] = openrouter_data

    # Query Kimi quota and attach
    kimi_data = query_kimi_quota()
    if kimi_data.get("status") != "online" and last_cache:
        cached_kimi = last_cache.get("data", {}).get("kimi") or last_cache.get("kimi")
        if cached_kimi and cached_kimi.get("status") == "online":
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
