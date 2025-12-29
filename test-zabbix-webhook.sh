#!/bin/bash

# Test script for Zabbix webhook integration
# Usage: ./test-zabbix-webhook.sh [port]

PORT=${1:-3000}
URL="http://localhost:${PORT}/webhook/zabbix"

echo "Testing Zabbix webhook at: $URL"
echo ""

# Test payload matching your Zabbix format
PAYLOAD='{
  "event_time": "2025-11-12 20:00:00",
  "alert_name": "High CPU usage on hypervisor",
  "severity": "High",
  "priority": "4",
  "metric_name": "system.cpu.util",
  "metric_value": "95.5",
  "trigger_expression": "{F0-HOST-104:system.cpu.util.avg(5m)}>90",
  "pending_duration": "5m 30s",
  "event_id": "12345",
  "hardware": "F0-HOST-104"
}'

echo "Sending test alert:"
echo "$PAYLOAD" | jq .
echo ""

# Send the webhook
RESPONSE=$(curl -s -w "\nHTTP_STATUS:%{http_code}" \
  -X POST \
  -H "Content-Type: application/json" \
  -H "X-Zabbix-Secret: ${ZABBIX_WEBHOOK_SECRET:-test-secret}" \
  -d "$PAYLOAD" \
  "$URL")

HTTP_BODY=$(echo "$RESPONSE" | sed -e 's/HTTP_STATUS\:.*//g')
HTTP_STATUS=$(echo "$RESPONSE" | tr -d '\n' | sed -e 's/.*HTTP_STATUS://')

echo "Response:"
echo "$HTTP_BODY"
echo ""
echo "HTTP Status: $HTTP_STATUS"

if [ "$HTTP_STATUS" -eq 200 ]; then
  echo "✅ Test passed! Check Slack for the alert."
else
  echo "❌ Test failed! Status code: $HTTP_STATUS"
  exit 1
fi
