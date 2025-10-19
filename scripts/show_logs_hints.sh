#!/usr/bin/env bash
# Quick pointers to check what happened

echo "== Where to look =="
echo "Gateway log (IN/OUT body sizes, tamper): logs/gateway.log"
echo "Payment log (401 on SAGE ON tamper):   logs/payment.log"
echo "Root log (forwarding decisions):        logs/root.log"
echo "Client log (frontend handling):         logs/client.log"
echo "Curl traces:                            logs/trace_sage_on.txt, logs/trace_sage_off.txt"
