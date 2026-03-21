#!/usr/bin/env bash
# cleanup.sh — Remove all VMSmith-managed resources from the system.
#
# What this script removes:
#   - All libvirt domains (VMs) whose names begin with the prefix managed by vmsmith
#   - All libvirt snapshots belonging to those domains
#   - The vmsmith-net libvirt NAT network (destroys + undefines it)
#   - All iptables DNAT/FORWARD rules added by vmsmith for port forwarding
#   - VM overlay disk directories under /var/lib/vmsmith/vms/
#   - Image files under /var/lib/vmsmith/images/
#   - The bbolt database (~/.vmsmith/vmsmith.db)
#   - The vmsmith log file (~/.vmsmith/vmsmith.log)
#   - The vmsmith PID file (/var/run/vmsmith.pid)
#   - Any stale dnsmasq process left by the vmsmith-net network
#
# Usage:
#   sudo ./scripts/cleanup.sh [--dry-run] [--force] [--network-name NAME]
#
# Options:
#   --dry-run         Print what would be done without making any changes.
#   --force           Skip the confirmation prompt.
#   --network-name    libvirt network name (default: vmsmith-net).
#   --images-dir      Image storage directory (default: /var/lib/vmsmith/images).
#   --vms-dir         VM overlay directory (default: /var/lib/vmsmith/vms).
#   --db-path         bbolt database path (default: ~/.vmsmith/vmsmith.db).
#   --log-file        Log file path (default: ~/.vmsmith/vmsmith.log).
#   --pid-file        Daemon PID file (default: /var/run/vmsmith.pid).

set -uo pipefail

# ---------------------------------------------------------------------------
# Defaults (match internal/config/config.go DefaultConfig())
# ---------------------------------------------------------------------------
NETWORK_NAME="vmsmith-net"
IMAGES_DIR="/var/lib/vmsmith/images"
VMS_DIR="/var/lib/vmsmith/vms"
HOME_DIR="${HOME:-$(getent passwd "$(id -un)" | cut -d: -f6)}"
DB_PATH="${HOME_DIR}/.vmsmith/vmsmith.db"
LOG_FILE="${HOME_DIR}/.vmsmith/vmsmith.log"
PID_FILE="/var/run/vmsmith.pid"

DRY_RUN=false
FORCE=false
ERRORS=0

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run)        DRY_RUN=true ;;
        --force)          FORCE=true ;;
        --network-name)   NETWORK_NAME="$2"; shift ;;
        --images-dir)     IMAGES_DIR="$2"; shift ;;
        --vms-dir)        VMS_DIR="$2"; shift ;;
        --db-path)        DB_PATH="$2"; shift ;;
        --log-file)       LOG_FILE="$2"; shift ;;
        --pid-file)       PID_FILE="$2"; shift ;;
        -h|--help)
            sed -n '3,30p' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *)
            echo "Unknown option: $1" >&2
            exit 1
            ;;
    esac
    shift
done

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
RED='\033[0;31m'
YELLOW='\033[1;33m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
BOLD='\033[1m'
RESET='\033[0m'

info()  { echo -e "${CYAN}[INFO]${RESET}  $*"; }
warn()  { echo -e "${YELLOW}[WARN]${RESET}  $*"; }
ok()    { echo -e "${GREEN}[OK]${RESET}    $*"; }
err()   { echo -e "${RED}[ERROR]${RESET} $*" >&2; ERRORS=$((ERRORS + 1)); }

run() {
    # run CMD ARGS... — executes only when not in dry-run mode.
    if $DRY_RUN; then
        echo -e "${YELLOW}[DRY-RUN]${RESET} $*"
    else
        "$@" || err "Command failed: $*"
    fi
}

# ---------------------------------------------------------------------------
# Pre-flight checks
# ---------------------------------------------------------------------------
if [[ $EUID -ne 0 ]] && ! $DRY_RUN; then
    warn "This script modifies iptables rules and libvirt networks — it typically requires root."
    warn "Re-run with sudo, or use --dry-run to preview."
    exit 1
fi

if ! command -v virsh &>/dev/null; then
    err "virsh not found. Install libvirt-clients / libvirt-bin first."
    exit 1
fi

# ---------------------------------------------------------------------------
# Confirmation prompt
# ---------------------------------------------------------------------------
if ! $FORCE && ! $DRY_RUN; then
    echo -e "${BOLD}${RED}"
    echo "  WARNING: This will PERMANENTLY REMOVE all VMSmith resources:"
    echo "    • All libvirt VMs managed by vmsmith (and their snapshots)"
    echo "    • The '${NETWORK_NAME}' libvirt network"
    echo "    • All VMSmith iptables port-forward rules"
    echo "    • All files under ${VMS_DIR}/ and ${IMAGES_DIR}/"
    echo "    • ${DB_PATH}"
    echo "    • ${LOG_FILE}"
    echo -e "${RESET}"
    read -r -p "Type 'yes' to continue: " CONFIRM
    if [[ "$CONFIRM" != "yes" ]]; then
        echo "Aborted."
        exit 0
    fi
fi

$DRY_RUN && info "(Dry-run mode — no changes will be made)"
echo ""

# ---------------------------------------------------------------------------
# 1. Stop the vmsmith daemon (if running)
# ---------------------------------------------------------------------------
info "=== Step 1: Stop vmsmith daemon ==="
if [[ -f "$PID_FILE" ]]; then
    DAEMON_PID=$(cat "$PID_FILE" 2>/dev/null || true)
    if [[ -n "$DAEMON_PID" ]] && kill -0 "$DAEMON_PID" 2>/dev/null; then
        info "Stopping vmsmith daemon (PID $DAEMON_PID)..."
        run kill -TERM "$DAEMON_PID"
        if ! $DRY_RUN; then
            # Give it up to 5 seconds to exit cleanly.
            for _ in 1 2 3 4 5; do
                kill -0 "$DAEMON_PID" 2>/dev/null || break
                sleep 1
            done
            if kill -0 "$DAEMON_PID" 2>/dev/null; then
                warn "Daemon did not stop in time; sending SIGKILL."
                run kill -KILL "$DAEMON_PID"
            fi
        fi
        ok "Daemon stopped."
    else
        info "PID file exists but daemon is not running (PID $DAEMON_PID)."
    fi
    run rm -f "$PID_FILE"
else
    info "No PID file found — daemon not running."
fi
echo ""

# ---------------------------------------------------------------------------
# 2. Destroy and undefine all VMSmith libvirt domains
# ---------------------------------------------------------------------------
info "=== Step 2: Remove libvirt VM domains ==="

# Collect all domains (running + stopped) that have vmsmith VM disk paths
# (disk under VMS_DIR). This avoids touching unrelated VMs.
DOMAINS=()
while IFS= read -r line; do
    DOM_NAME=$(echo "$line" | awk '{print $2}')
    [[ -z "$DOM_NAME" || "$DOM_NAME" == "Name" ]] && continue
    # Check if the domain has any disk under our VMS_DIR
    DISK_XML=$(virsh domblklist "$DOM_NAME" --details 2>/dev/null || true)
    if echo "$DISK_XML" | grep -q "$VMS_DIR"; then
        DOMAINS+=("$DOM_NAME")
    fi
done < <(virsh list --all 2>/dev/null)

if [[ ${#DOMAINS[@]} -eq 0 ]]; then
    info "No VMSmith-managed domains found."
else
    for DOM in "${DOMAINS[@]}"; do
        info "Processing domain: $DOM"

        # Destroy (power off) if running
        DOM_STATE=$(virsh domstate "$DOM" 2>/dev/null || echo "undefined")
        if [[ "$DOM_STATE" == "running" || "$DOM_STATE" == "paused" ]]; then
            info "  Destroying (power-off) $DOM..."
            run virsh destroy "$DOM"
        fi

        # Undefine with snapshot metadata cleanup
        info "  Undefining $DOM (with snapshots)..."
        if $DRY_RUN; then
            echo -e "${YELLOW}[DRY-RUN]${RESET} virsh undefine $DOM --remove-all-storage --snapshots-metadata"
        else
            virsh undefine "$DOM" --snapshots-metadata 2>/dev/null \
                || err "Failed to undefine domain $DOM"
        fi
        ok "  Domain $DOM removed."
    done
fi
echo ""

# ---------------------------------------------------------------------------
# 3. Remove VMSmith iptables port-forwarding rules
# ---------------------------------------------------------------------------
info "=== Step 3: Remove iptables port-forward rules ==="

# VMSmith inserts DNAT rules in nat/PREROUTING and ACCEPT rules in
# filter/FORWARD that target 192.168.100.x (the vmsmith-net subnet).
VMSMITH_SUBNET_PREFIX="192.168.100."

remove_iptables_rules() {
    local TABLE="$1"
    local CHAIN="$2"
    local GREP_PATTERN="$3"

    if ! iptables -t "$TABLE" -n -L "$CHAIN" &>/dev/null; then
        return
    fi

    # Collect line numbers in reverse order so deletions don't shift indices.
    mapfile -t RULE_NUMS < <(
        iptables -t "$TABLE" -n -L "$CHAIN" --line-numbers 2>/dev/null \
            | grep -E "$GREP_PATTERN" \
            | awk '{print $1}' \
            | sort -rn
    )

    if [[ ${#RULE_NUMS[@]} -eq 0 ]]; then
        return
    fi

    info "  Found ${#RULE_NUMS[@]} rule(s) in ${TABLE}/${CHAIN} targeting vmsmith subnet."
    for NUM in "${RULE_NUMS[@]}"; do
        if $DRY_RUN; then
            local RULE_LINE
            RULE_LINE=$(iptables -t "$TABLE" -n -L "$CHAIN" --line-numbers 2>/dev/null | grep "^${NUM} ")
            echo -e "${YELLOW}[DRY-RUN]${RESET} iptables -t $TABLE -D $CHAIN $NUM  # $RULE_LINE"
        else
            iptables -t "$TABLE" -D "$CHAIN" "$NUM" \
                || err "Failed to delete rule $NUM from ${TABLE}/${CHAIN}"
        fi
    done
}

if command -v iptables &>/dev/null; then
    remove_iptables_rules nat    PREROUTING "$VMSMITH_SUBNET_PREFIX"
    remove_iptables_rules filter FORWARD    "$VMSMITH_SUBNET_PREFIX"
    ok "iptables cleanup complete."
else
    warn "iptables not found; skipping port-forward rule cleanup."
fi
echo ""

# ---------------------------------------------------------------------------
# 4. Destroy and undefine the vmsmith-net libvirt network
# ---------------------------------------------------------------------------
info "=== Step 4: Remove libvirt network '${NETWORK_NAME}' ==="

NET_STATE=$(virsh net-info "$NETWORK_NAME" 2>/dev/null | grep -i "^Active:" | awk '{print $2}' || echo "no")
if [[ "$NET_STATE" == "yes" ]]; then
    info "  Destroying network ${NETWORK_NAME}..."
    run virsh net-destroy "$NETWORK_NAME"
fi

if virsh net-info "$NETWORK_NAME" &>/dev/null; then
    info "  Undefining network ${NETWORK_NAME}..."
    run virsh net-undefine "$NETWORK_NAME"
    ok "  Network ${NETWORK_NAME} removed."
else
    info "  Network ${NETWORK_NAME} not found — already removed or never created."
fi
echo ""

# ---------------------------------------------------------------------------
# 5. Kill stale dnsmasq process (if any)
# ---------------------------------------------------------------------------
info "=== Step 5: Kill stale dnsmasq for '${NETWORK_NAME}' ==="
DNSMASQ_PID_FILE="/run/libvirt/network/${NETWORK_NAME}.pid"
if [[ -f "$DNSMASQ_PID_FILE" ]]; then
    DNSMASQ_PID=$(cat "$DNSMASQ_PID_FILE" 2>/dev/null || true)
    if [[ -n "$DNSMASQ_PID" ]] && kill -0 "$DNSMASQ_PID" 2>/dev/null; then
        info "  Sending SIGTERM to stale dnsmasq (PID $DNSMASQ_PID)..."
        run kill -TERM "$DNSMASQ_PID"
        ok "  dnsmasq stopped."
    else
        info "  dnsmasq PID file exists but process is not running."
    fi
    run rm -f "$DNSMASQ_PID_FILE"
else
    info "  No stale dnsmasq PID file found."
fi
echo ""

# ---------------------------------------------------------------------------
# 6. Remove VM overlay disk directories
# ---------------------------------------------------------------------------
info "=== Step 6: Remove VM disk directories under ${VMS_DIR}/ ==="
if [[ -d "$VMS_DIR" ]]; then
    if $DRY_RUN; then
        echo -e "${YELLOW}[DRY-RUN]${RESET} rm -rf ${VMS_DIR}/*"
        ls -1 "$VMS_DIR" 2>/dev/null | while IFS= read -r entry; do
            echo -e "${YELLOW}[DRY-RUN]${RESET}   would remove: ${VMS_DIR}/${entry}"
        done
    else
        COUNT=$(find "$VMS_DIR" -mindepth 1 -maxdepth 1 | wc -l)
        find "$VMS_DIR" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
        ok "Removed ${COUNT} entries from ${VMS_DIR}/."
    fi
else
    info "Directory ${VMS_DIR}/ does not exist — skipping."
fi
echo ""

# ---------------------------------------------------------------------------
# 7. Remove image files
# ---------------------------------------------------------------------------
info "=== Step 7: Remove image files under ${IMAGES_DIR}/ ==="
if [[ -d "$IMAGES_DIR" ]]; then
    if $DRY_RUN; then
        echo -e "${YELLOW}[DRY-RUN]${RESET} rm -rf ${IMAGES_DIR}/*"
        ls -1 "$IMAGES_DIR" 2>/dev/null | while IFS= read -r entry; do
            echo -e "${YELLOW}[DRY-RUN]${RESET}   would remove: ${IMAGES_DIR}/${entry}"
        done
    else
        COUNT=$(find "$IMAGES_DIR" -mindepth 1 -maxdepth 1 | wc -l)
        find "$IMAGES_DIR" -mindepth 1 -maxdepth 1 -exec rm -rf {} +
        ok "Removed ${COUNT} entries from ${IMAGES_DIR}/."
    fi
else
    info "Directory ${IMAGES_DIR}/ does not exist — skipping."
fi
echo ""

# ---------------------------------------------------------------------------
# 8. Remove the bbolt database
# ---------------------------------------------------------------------------
info "=== Step 8: Remove database ${DB_PATH} ==="
if [[ -f "$DB_PATH" ]]; then
    run rm -f "$DB_PATH"
    ok "Database removed."
else
    info "Database not found — skipping."
fi
echo ""

# ---------------------------------------------------------------------------
# 9. Remove the log file
# ---------------------------------------------------------------------------
info "=== Step 9: Remove log file ${LOG_FILE} ==="
if [[ -f "$LOG_FILE" ]]; then
    run rm -f "$LOG_FILE"
    ok "Log file removed."
else
    info "Log file not found — skipping."
fi
echo ""

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo "========================================"
if $DRY_RUN; then
    echo -e "${YELLOW}Dry-run complete — no changes were made.${RESET}"
elif [[ $ERRORS -eq 0 ]]; then
    echo -e "${GREEN}${BOLD}Cleanup complete. All VMSmith resources removed.${RESET}"
else
    echo -e "${RED}${BOLD}Cleanup finished with ${ERRORS} error(s). Review output above.${RESET}"
    exit 1
fi
echo "========================================"
