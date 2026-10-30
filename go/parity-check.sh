#!/usr/bin/env bash
# Паритет-чек Go-CLI vs C#-CLI: сравнивает ВСЕ числовые поля бакетов
# (used_percent, used_fraction) и reset_in из --json обеих реализаций.
# Прогоны идут подряд, чтобы live-окна не успевали сдвинуться.
#
# Usage: go/parity-check.sh <go-binary> <cs-binary> <env-file> [config-file]
set -euo pipefail

GO_BIN="${1:?go binary}"
CS_BIN="${2:?cs binary}"
ENV_FILE="${3:?env file}"
CONFIG_ARG=()
[ $# -ge 4 ] && CONFIG_ARG=(--config "$4")

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
cp "$ENV_FILE" "$WORK/.env"

run() { # <binary> <out>
    env --unset=NO_COLOR "$1" --env "$WORK/.env" "${CONFIG_ARG[@]}" --no-color --json 2>/dev/null \
        | tr -d '\r' > "$2"
}

run "$GO_BIN" "$WORK/go.json"
run "$CS_BIN" "$WORK/cs.json"

fail=0

# --- числовые поля ---
for impl in go cs; do
    jq -r 'paths(scalars) as $p
           | select($p[-1] == "used_percent" or $p[-1] == "used_fraction")
           | [($p|join(".")), getpath($p)] | @tsv' "$WORK/$impl.json" | sort > "$WORK/$impl.num"
done
echo "== used_percent / used_fraction =="
while IFS=$'\t' read -r field goval csval; do
    verdict="$(awk -v g="$goval" -v c="$csval" 'BEGIN{d=g-c; if(d<0)d=-d; print (d<=0.15)?"OK":"DIFF"}')"
    [ "$verdict" = "OK" ] || fail=1
    printf "%-45s Go=%-8s C#=%-8s %s\n" "$field" "$goval" "$csval" "$verdict"
done < <(join -j 1 -t $'\t' "$WORK/go.num" "$WORK/cs.num")

# --- reset_in ---
for impl in go cs; do
    jq -r 'paths(scalars) as $p
           | select($p[-1] == "reset_in")
           | [($p|join(".")), getpath($p)] | @tsv' "$WORK/$impl.json" | sort > "$WORK/$impl.ri"
done
echo "== reset_in =="
while IFS=$'\t' read -r field goval csval; do
    verdict="OK"; [ "$goval" = "$csval" ] || { verdict="DIFF"; fail=1; }
    printf "%-45s Go=%-8s C#=%-8s %s\n" "$field" "$goval" "$csval" "$verdict"
done < <(join -j 1 -t $'\t' "$WORK/go.ri" "$WORK/cs.ri")

# --- статусы ---
for impl in go cs; do
    jq -r '.status as $s | .providers | to_entries[] | "\(.key)\t\(.value.status)"' "$WORK/$impl.json" | sort > "$WORK/$impl.st"
done
echo "== status =="
while IFS=$'\t' read -r id goval csval; do
    verdict="OK"; [ "$goval" = "$csval" ] || { verdict="DIFF"; fail=1; }
    printf "%-45s Go=%-8s C#=%-8s %s\n" "$id" "$goval" "$csval" "$verdict"
done < <(join -j 1 -t $'\t' "$WORK/go.st" "$WORK/cs.st")

if [ "$fail" -eq 0 ]; then
    echo "PARITY: ALL OK"
else
    echo "PARITY: DIFFERENCES FOUND"
    exit 2
fi
