#!/usr/bin/env bash
# robot-platform-service Management Plane v2 本地合同演示
# 前置:服务已启动(go run ./cmd/platformd -addr :9100 -db data/platform.db)
# 用法:bash scripts/demo-management-plane.sh
set -euo pipefail

BASE="${BASE:-http://127.0.0.1:9100}"
JQ() { python3 -m json.tool 2>/dev/null || cat; }

echo "== Act 1: 健康检查 =="
curl -s "$BASE/v2/health" | JQ

echo
echo "== Act 2: 注册 Robot(panda 域, simulation 机器人) =="
ROBOT=$(curl -s -X POST "$BASE/v2/robots" \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"panda-sim","domain":"panda","embodiment":"simulation"}')
echo "$ROBOT" | JQ
ROBOT_ID=$(echo "$ROBOT" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")

echo
echo "== Act 3: 在 Robot 下注册 Device(compute host + lidar sensor) =="
HOST_DEV=$(curl -s -X POST "$BASE/v2/robots/$ROBOT_ID/devices" \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"jetson-orin","device_class":"compute","manufacturer":"NVIDIA","model":"AGX Orin"}')
echo "$HOST_DEV" | JQ
HOST_ID=$(echo "$HOST_DEV" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")

curl -s -X POST "$BASE/v2/robots/$ROBOT_ID/devices" \
  -H 'Content-Type: application/json' \
  -d '{"display_name":"lidar-front","device_class":"sensor","domain_type":"lidar_2d"}' | JQ

echo
echo "== Act 4: 注册 Runtime(control_runtime, 部署在 compute device 上, 心跳间隔 100ms) =="
RUNTIME=$(curl -s -X POST "$BASE/v2/robots/$ROBOT_ID/runtimes" \
  -H 'Content-Type: application/json' \
  -d "{\"display_name\":\"rcrd-main\",\"runtime_role\":\"control_runtime\",\"component\":\"rcrd\",\"host_device_id\":\"$HOST_ID\",\"heartbeat_interval_ms\":100}")
echo "$RUNTIME" | JQ
RUNTIME_ID=$(echo "$RUNTIME" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")

echo
echo "== Act 5: 查询 Runtime liveness(无 session → unknown) =="
curl -s "$BASE/v2/runtimes/$RUNTIME_ID/liveness" | JQ

echo
echo "== Act 6: 创建 RuntimeSession(模拟进程启动)并上报心跳 =="
curl -s -X POST "$BASE/v2/runtimes/$RUNTIME_ID/sessions" \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"boot-1","software_version_ref":"unknown","started_at_reported":1000000}' | JQ

echo
echo "  >> seq 1..3(seq 在 session 内严格递增) =="
for i in 1 2 3; do
  curl -s -X POST "$BASE/v2/runtimes/$RUNTIME_ID/sessions/boot-1/heartbeats" \
    -H 'Content-Type: application/json' -d "{\"seq\":$i}" | JQ
  sleep 0.05
done

echo
echo "== Act 7: 查询 liveness(刚收到心跳 → online) =="
curl -s "$BASE/v2/runtimes/$RUNTIME_ID/liveness" | JQ

echo
echo "== Act 8: 等待 400ms(age > 3×100ms → stale) =="
sleep 0.4
curl -s "$BASE/v2/runtimes/$RUNTIME_ID/liveness" | JQ

echo
echo "== Act 9: 结束 session → offline =="
curl -s -X POST "$BASE/v2/runtimes/$RUNTIME_ID/sessions/boot-1/end" \
  -H 'Content-Type: application/json' | JQ
curl -s "$BASE/v2/runtimes/$RUNTIME_ID/liveness" | JQ

echo
echo "== Act 10: 新 session(boot-2)→ seq 从 1 重新开始 =="
curl -s -X POST "$BASE/v2/runtimes/$RUNTIME_ID/sessions" \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"boot-2","software_version_ref":"v2.0.0"}' | JQ
curl -s -X POST "$BASE/v2/runtimes/$RUNTIME_ID/sessions/boot-2/heartbeats" \
  -H 'Content-Type: application/json' -d '{"seq":1}' | JQ
curl -s "$BASE/v2/runtimes/$RUNTIME_ID/liveness" | JQ

echo
echo "  >> boot-1 迟到心跳 → accepted 但 session_state=ended/superseded(非 current) =="
curl -s -X POST "$BASE/v2/runtimes/$RUNTIME_ID/sessions/boot-1/heartbeats" \
  -H 'Content-Type: application/json' -d '{"seq":4}' | JQ

echo
echo "== Act 11: 创建 v1 Task + v2 Run(typed artifact_ref) =="
TASK=$(curl -s -X POST "$BASE/v1/tasks" \
  -H 'Content-Type: application/json' \
  -d '{"domain":"panda","kind":"collect","target":"red-box"}')
TASK_ID=$(echo "$TASK" | python3 -c "import sys,json; print(json.load(sys.stdin)['id'])")
echo "$TASK" | JQ

curl -s -X POST "$BASE/v2/runs" \
  -H 'Content-Type: application/json' \
  -d "{\"task_id\":\"$TASK_ID\",\"robot_id\":\"$ROBOT_ID\",\"runtime_id\":\"$RUNTIME_ID\",\"session_id\":\"boot-2\",\"result\":\"succeeded\",\"artifact_ref\":{\"type\":\"episode\",\"uri\":\"release/e2_scene_preflight_v1\",\"hash_sha256\":\"abc123def456\",\"producer_repo\":\"ros2-arm-teleoperation-suite\",\"producer_version\":\"v2.0.0\"}}" | JQ

echo
echo "== Act 12: 查询 Session 历史列表 =="
curl -s "$BASE/v2/runtimes/$RUNTIME_ID/sessions" | JQ

echo
echo "== 完成。演示要点:Robot/Device/Runtime 三身份拆分、session 内 seq 递增、新 session seq 重置、迟到心跳只审计不更新 liveness、typed artifact_ref =="
