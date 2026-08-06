#!/usr/bin/env bash
# robot-platform-service 演示脚本(Phase 1 最小闭环)
# 前置:服务已启动(go run ./cmd/platformd -addr :9100 -db data/platform.db)
# 用法:bash scripts/demo.sh
set -euo pipefail

BASE="${BASE:-http://127.0.0.1:9100}"
JQ() { python3 -m json.tool 2>/dev/null || cat; }

echo "== 1. 健康检查 =="
curl -s "$BASE/v1/health" | JQ

echo
echo "== 2. 注册两个设备(panda-sim / amr-sim,心跳间隔 100ms 便于演示状态变化)=="
curl -s -X POST "$BASE/v1/devices" \
  -H 'Content-Type: application/json' \
  -d '{"name":"panda-sim","kind":"panda","version":"v0.1.0","heartbeat_interval_ms":100}' | JQ
curl -s -X POST "$BASE/v1/devices" \
  -H 'Content-Type: application/json' \
  -d '{"name":"amr-sim","kind":"amr","version":"v0.1.0","heartbeat_interval_ms":100}' | JQ

DEV_ID=$(curl -s "$BASE/v1/devices" | python3 -c "import sys,json; print(json.load(sys.stdin)['devices'][0]['id'])")
echo
echo "== 3. 对 $DEV_ID 发送心跳(seq 1..3;seq 必须严格递增,重复 seq 会 409)=="
for i in 1 2 3; do
  curl -s -X POST "$BASE/v1/devices/$DEV_ID/heartbeats" \
    -H 'Content-Type: application/json' -d "{\"seq\":$i}" | JQ
  sleep 0.2
done

echo
echo "== 4. 查询设备状态(刚发完心跳应为 ok)=="
curl -s "$BASE/v1/devices" | JQ

echo
echo "== 5. 创建任务与运行记录(跨域信封:amr 域任务 + artifact 指针)=="
TASK=$(curl -s -X POST "$BASE/v1/tasks" \
  -H 'Content-Type: application/json' \
  -d '{"domain":"amr","kind":"transport","target":"station_a"}')
TASK_ID=$(echo "$TASK" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
echo "$TASK" | JQ
curl -s -X POST "$BASE/v1/runs" \
  -H 'Content-Type: application/json' \
  -d "{\"task_id\":\"$TASK_ID\",\"device_id\":\"$DEV_ID\",\"result\":\"succeeded\",\"artifact_ref\":\"release/e2_v1\"}" | JQ

echo
echo "== 6. 等待 500ms 让心跳过期(3×100ms=300ms → stale;6×100ms=600ms → missing)=="
sleep 0.5
curl -s "$BASE/v1/devices" | JQ

echo
echo "== 完成。演示要点:信封统一(amr/panda 任务并存)、seq 严格递增、ok→stale→missing 判定 =="
