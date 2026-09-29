#!/bin/sh
# smoke-k0s.sh — assert the single-node k0s substrate is up and, if the node serves
# anything, that it is serving. Run ON the box.
#
# RIVER-owned checks: the k0s API and the shipped CoreDNS. Workload checks come from the
# node, not from this file:
#   SMOKE_DEPLOYMENTS  space-separated ns/name Deployments that must have >= 1 available
#                      replica (default: none)
#   SMOKE_HOSTS        names to probe over HTTPS on 127.0.0.1 (default: the list in
#                      /etc/runink/app-hosts, if any)
#   /usr/local/share/runink/core/smoke.sh  a downstream payload's own smoke test, run last
#                      when present
set -u

pass=0; fail=0
ok()  { echo "  OK   $*"; pass=$((pass+1)); }
bad() { echo "  FAIL $*" >&2; fail=$((fail+1)); }

KC() { k0s kubectl --kubeconfig /var/lib/k0s/pki/admin.conf "$@"; }

echo "== smoke-k0s =="

# k0s API up.
KC get --raw='/readyz' >/dev/null 2>&1 && ok "k0s API ready" || bad "k0s API not ready"

check_deploy() { # ns deploy
	rep="$(KC -n "$1" get deploy "$2" -o jsonpath='{.status.availableReplicas}' 2>/dev/null || echo 0)"
	[ "${rep:-0}" -ge 1 ] && ok "deploy up: $1/$2 ($rep)" || bad "deploy down/missing: $1/$2"
}
# The image's own CoreDNS (var/lib/k0s/manifests/runink-coredns).
check_deploy kube-system coredns
for d in ${SMOKE_DEPLOYMENTS:-}; do
	check_deploy "${d%%/*}" "${d#*/}"
done

HOSTS="${SMOKE_HOSTS:-}"
[ -z "$HOSTS" ] && [ -r /etc/runink/app-hosts ] && \
	HOSTS=$(sed -e 's/#.*//' /etc/runink/app-hosts | tr '\n' ' ')
for host in $HOSTS; do
	code="$(curl -sk -o /dev/null -w '%{http_code}' -H "Host: $host" https://127.0.0.1/ 2>/dev/null || echo 000)"
	case "$code" in
		2??|3??) ok "$host HTTPS responds ($code)";;
		*) bad "$host HTTPS not responding ($code)";;
	esac
done

PAYLOAD_SMOKE=/usr/local/share/runink/core/smoke.sh
if [ -x "$PAYLOAD_SMOKE" ]; then
	"$PAYLOAD_SMOKE" && ok "payload smoke test" || bad "payload smoke test ($PAYLOAD_SMOKE)"
fi

echo "== smoke-k0s: $pass passed, $fail failed =="
[ "$fail" -eq 0 ]
