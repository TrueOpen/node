#!/usr/bin/env bash
#
# check_split_import_direction.sh — enforce the dependency direction between the
# three business modules.
#
# The hub owns registry, staking and rewards; the task module owns the task
# lifecycle; shared holds the canonical encoding both depend on. The direction is
# one-way by design: the hub must be able to run without knowing that tasks
# exist, so a future change that makes the hub reach into task state fails here
# rather than in a consensus-divergence report.
#
# Rules:
#   1. x/hub must NOT import x/task.
#   2. x/task may import x/hub/types (the HubKeeper interface) but MUST NOT
#      import x/hub/keeper.
#   3. x/task must NOT touch the hub store key.
#   4. x/hub must NOT touch the task store key.
#
# Exit 0 = clean. Exit 1 = a violation, with file:line evidence.
#
# Guard: re-exec under bash if invoked as `sh script.sh` on dash.
if [ -z "${BASH_VERSION:-}" ]; then exec bash "$0" "$@"; fi
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"

MOD="github.com/TrueOpen/node"
HUB=x/hub
TASK=x/task
SHARED=x/shared

pass() { printf '\033[1;32m[ok]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[FAIL]\033[0m %s\n' "$*"; }

violations=0

for dir in "$HUB" "$TASK" "$SHARED"; do
    if [ ! -d "$dir" ]; then
        fail "$dir is missing; the three business modules must all be present."
        exit 1
    fi
done

# Rule 1: hub must not import task.
if hits=$(grep -rn --include='*.go' "$MOD/$TASK" "$HUB" 2>/dev/null); then
    fail "rule 1: $HUB imports $TASK (hub must be unaware of task):"
    echo "$hits"
    violations=$((violations + 1))
else
    pass "rule 1: $HUB does not import $TASK"
fi

# Rule 2: task may import hub/types, but not hub/keeper.
if hits=$(grep -rn --include='*.go' "$MOD/$HUB/keeper" "$TASK" 2>/dev/null); then
    fail "rule 2: $TASK imports $HUB/keeper (must cross only via $HUB/types.HubKeeper):"
    echo "$hits"
    violations=$((violations + 1))
else
    pass "rule 2: $TASK does not import $HUB/keeper"
fi

# Rule 3: task must not reference the hub store-key prefix. This matches the
# bare store-key literal, not the .../x/hub/types import path, which is the
# legitimate way to reach the HubKeeper interface.
if hits=$(grep -rn --include='*.go' '"hub"' "$TASK" 2>/dev/null); then
    fail "rule 3: $TASK references the hub store-key literal \"hub\":"
    echo "$hits"
    violations=$((violations + 1))
else
    pass "rule 3: $TASK does not touch the hub store key"
fi

# Rule 4: hub must not reference the task store-key prefix.
if hits=$(grep -rn --include='*.go' '"task"' "$HUB" 2>/dev/null); then
    fail "rule 4: $HUB references the task store-key literal \"task\":"
    echo "$hits"
    violations=$((violations + 1))
else
    pass "rule 4: $HUB does not touch the task store key"
fi

echo
if [ "$violations" -eq 0 ]; then
    pass "all module dependency-direction rules satisfied"
    exit 0
fi
fail "$violations rule(s) violated"
exit 1
