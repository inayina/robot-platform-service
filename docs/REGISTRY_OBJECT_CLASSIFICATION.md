# Registry 对象分类账

- 状态：`D1 complete / Design evidence only`
- 分类日期：2026-08-06
- 作用范围：八仓中的 Robot、Device、Runtime 候选及明确排除项
- 依据：[Robot / Device / Runtime 拆分合同](ROBOT_DEVICE_RUNTIME_CONTRACT.md)
- 非本文件范围：写数据库、迁移 v1 数据、创建 Registry 记录、接入 Heartbeat 或修改其它仓库

这份分类账回答“什么对象有资格进入 Platform Registry”，不回答“这些对象是否已经接入”。当前 `robot-platform-service` 与其它七仓仍是 `Not integrated`；表中的 `Eligible` 只表示对象语义成立，不表示已有 API producer、Runtime heartbeat 或运行证据。

## 1. 证据快照

分类只使用仓库 README、架构/边界文档、部署文件、launch/entry point 和配置；没有从名称、topic 或目录结构猜身份。

| 仓库 | 审计 revision | 证据状态 |
|---|---|---|
| `robot-platform-service` | `902614f` + 当前本地 Management Plane 草案 | `main` 仍是 Phase 1；本地草案未重新验证、未发布 |
| `robot-control-runtime` | `385bc04` | 本地 `main` clean |
| `robot-ops-dashboard` | `d058f57` | GitHub `main`；本地未提交文档不作为已发布事实 |
| `amr_warehouse_navigation` | `c57ae1c` | GitHub `main` |
| `ros2-robot-digital-twin` | `56dd83e` | GitHub `main` |
| `ros2-arm-teleoperation-suite` | `f3a7607` | 本地 `main` clean |
| `robot-arm-episode-data-lab` | `381cf71` | 本地 `main` clean |
| `ros2-moveit-pybullet-bridge` | `985c8da` | 本地 `main` clean |

证据入口见文末。revision 只冻结本次分类依据，不证明所有仓库形成了一个已部署系统。

## 2. 分类规则

### 2.1 Classification

| 分类 | 必须满足 |
|---|---|
| `Robot` | 可接受领域 Task；拥有独立 physical/simulation 执行状态；Runtime 重启不改变身份 |
| `Device` | 只属于一台 Robot；是可替换、可追踪的硬件或仿真端点；普通 firmware 升级不改变身份 |
| `Runtime` | 只服务一台 Robot；可独立部署、监督、升级并提供管理面身份；每次进程启动产生新 RuntimeSession |
| `Not Registry` | repo、topic、ROS node、进程内部组件、UI、artifact、batch job、测试夹具或没有实际管理查询需求的对象 |

### 2.2 Registry disposition

| 状态 | 含义 |
|---|---|
| `Eligible` | 分类和父对象都明确；未来通过 Gate 后可以登记 |
| `Blocked` | 分类明确，但缺少强制父 Robot、稳定部署边界或管理面 producer；现在禁止登记 |
| `Exclude` | 当前职责下明确不进入 Registry |
| `Mock-only` | 仅用于 curl、测试或录屏演示；不得迁移成真实资产 |

`Blocked` 不等于 `Ambiguous`。例如 STM32F411 明确是 Device，但当前 bench 没有符合合同的父 Robot，所以结论是“Device / Blocked”，不是把 bench 猜成 Robot。

## 3. Robot 分类

表中的 candidate key 是分类标签，不是建议直接写入数据库的 canonical ID。

| Candidate | 来源 | Classification | 身份寿命与权威 | Disposition | 决策 |
|---|---|---|---|---|---|
| `amr-gazebo-warehouse` | AMR 仓库中的 Gazebo 差速 AMR | `Robot` / `amr` / `simulation` | Platform 管 identity；AMR executor/Nav2 管 readiness、执行与任务终态。软件重启不换 Robot；并发或独立 simulation instance 必须新建 Robot | `Eligible` | 它是高层 transport Task 的实际目标，不是 `navigation.launch.py`、map 或 Gazebo process |
| `panda-mujoco` | Panda 上游默认 MuJoCo 执行链 | `Robot` / `panda` / `simulation` | Platform 管 identity；上游 safety 与 Task GT 管执行真值。Runtime/recorder 重启不换 Robot | `Eligible` | MuJoCo 物理状态形成独立执行实体 |
| `panda-isaac` | Panda 上游有界 Isaac adapter/external process | `Robot` / `panda` / `simulation` | 与 MuJoCo 分开；上游连续 Task GT 仍是权威。每个独立 Isaac simulation instance 有独立 Robot identity | `Eligible` | 不得用同一个 `panda-sim` ID 混合 MuJoCo 与 Isaac 证据；learned policy 当前 Hold 不影响身份分类 |
| `panda-pybullet` | Panda 下游 PyBullet replay | `Robot` / `panda` / `simulation` | Platform 管 identity；下游只管 replay/risk，不能覆盖上游 Task GT | `Eligible` | PyBullet 的状态与执行 session 独立于 MuJoCo/Isaac，必须是另一台 simulation Robot |
| `digital-twin-motor-bench` | STM32/ESP32/单 N20 bench | `Not Registry`（作为 Robot） | 当前只有单电机与传感/桥接链，没有可接受领域 Task 的完整机器人资产 | `Exclude` | 不把 test bench、轮端速度或仓库名称包装成 Robot；未来出现明确 physical Robot 后再为设备指定父对象 |
| `physical-panda` | 三仓文档中的未来实机方向 | `Not Registry`（当前） | 仓库明确没有真实 Panda 部署 | `Exclude` | 不为尚不存在的实机预建资产记录 |
| `iiwa7` / `planar_2dof` | 下游 Legacy/CI profiles | `Not Registry` | 仅 legacy regression 或 CI smoke，不属于当前 Panda 作品集主线 | `Exclude` | 测试 profile 不是运营资产 |
| v1 `panda-sim` / `amr-sim` | `scripts/demo.sh` | 概念上像 Robot，但记录本身是 `Mock-only` | 名称不足以区分 MuJoCo/Isaac/PyBullet，也没有外部权威 ID | `Mock-only` | 不自动映射到任何目标 Robot；需要演示数据时可删除并重建，不做迁移 |
| v2 demo `panda-sim` | `scripts/demo-management-plane.sh` | `Mock-only` | 同时虚构 Jetson、LiDAR 和 `rcrd` 组合，不对应八仓已证明的真实部署 | `Mock-only` | 只验证 HTTP 合同，不能进入作品集 inventory |

### Robot 结论

- 当前可成立的目标 Robot 候选只有四个 simulation identity：AMR Gazebo、Panda MuJoCo、Panda Isaac、Panda PyBullet。
- 当前没有可登记的 physical Robot。
- Digital Twin 与 `robot-control-runtime` 的硬件不能为了满足 `device.robot_id NOT NULL` 而反向发明一台 Robot。
- Robot online/readiness/task success 仍由 Domain 表达，不能从 Runtime heartbeat 推断。

## 4. Device 分类

| Candidate | 父 Robot | Classification | Version / condition 权威 | Disposition | 决策 |
|---|---|---|---|---|---|
| Gazebo AMR base/controller endpoint | `amr-gazebo-warehouse` | `Device(controller)` | AMR simulation/domain；Platform 只存静态 identity 和来源投影 | `Eligible` | 可表达 AMR 的仿真底盘端点；不保存 `/cmd_vel`、里程计或 controller 参数 |
| Gazebo AMR lidar endpoint | `amr-gazebo-warehouse` | `Device(sensor)` | AMR simulation/domain | `Eligible` | 可表达可替换的仿真 sensor endpoint；不保存 LaserScan 样本 |
| STM32F411 controller | 尚不存在 | `Device(controller)` | firmware version 归 Digital Twin；condition 由固件/bridge 来源上报 | `Blocked: missing parent Robot` | FreeRTOS task、UART line 和 `State:n` 不是额外 Device |
| ESP32-S3 DevKitC-1 | 尚不存在 | `Device(controller)` | firmware version 归 Digital Twin；本地电机安全状态由 ESP32 权威 | `Blocked: missing parent Robot` | 它是硬件控制/bridge 端点，不是 Platform Runtime；固件重启不创建新 Device |
| MPU6050 | 尚不存在 | `Device(sensor)` | STM32 驱动/固件拥有量测与 condition | `Blocked: missing parent Robot` | IMU topic、四元数和采样流不是 Device identity |
| TB6612FNG | 尚不存在 | `Device(controller)` | ESP32/STM32 固件拥有输出与保护语义 | `Blocked: missing parent Robot` | PWM channel、GPIO 和 MQTT command 不是 Device |
| single N20 motor + encoder | 尚不存在 | `Device(actuator)` | ESP32 motor controller 拥有 rpm/PID/fault 事实 | `Blocked: missing parent Robot` | 当前只是单轮 bench，不是整车 Robot，也不把 wheel speed 当 Robot speed |
| Orange Pi 4 Pro 4GB | 尚不存在 | `Device(compute)` | hardware inventory 由 Platform；OS/runtime condition 由部署来源上报 | `Blocked: missing parent Robot` | 已有板上构建/部署证据，但当前厂商内核无 CAN，不能写成已承载 active `rcrd` |
| MuJoCo/Isaac/PyBullet arm、gripper、virtual drive | 相应 Panda simulation | `Not Registry`（当前） | 仍由各 simulation domain 管理 | `Exclude` | 当前没有独立资产替换、版本或 condition 查询需求；ROS node/virtual driver 数量不等于 Device 数量 |
| ThinkPad development host | 无 | `Not Registry` | 开发与 benchmark 基础设施 | `Exclude` | 不属于某台 Robot，不能复制进多个 Robot inventory |
| `vcan0`、SocketCAN fd、CAN frame、ROS/MQTT topic | 无 | `Not Registry` | 协议或 OS 资源 | `Exclude` | 不是可替换资产身份 |
| v2 demo `jetson-orin` / `lidar-front` | mock `panda-sim` | `Mock-only` | 无仓库部署或资产证据 | `Mock-only` | 不迁移、不当作已拥有硬件 |

### Device 结论

- AMR 的仿真 base/controller 与 lidar 语义成立，但尚无 Platform producer。
- Digital Twin 五个硬件对象和 Orange Pi 的 Device 分类明确，却都因缺少父 Robot 被阻止登记。
- 当前 Panda 三仓不需要为了“拓扑完整”登记仿真 arm、gripper 或七个 virtual servo。
- firmware、topic、sensor sample、PID/PWM 和 CAN heartbeat 都不能进入 Device Registry 字段。

## 5. Runtime 分类

Runtime 的粒度是稳定部署实例，不是“每个进程/ROS node 一行”。同一 top-level launch 内的内部节点默认随一个部署单元管理，除非以后出现第二个独立版本、监督和 liveness 查询需求。

| Runtime candidate | 父 Robot | Role / component | Session Version | Heartbeat producer | Disposition |
|---|---|---|---|---|---|
| `rcrd` deployment instance | 尚不存在 | `control_runtime` / `rcrd` | 精确 release/build + Git SHA，绑定 RuntimeSession | 未来独立 management adapter；不能使用 CAN NodeHeartbeat | `Blocked: missing parent Robot + no Platform producer` |
| AMR task executor deployment | `amr-gazebo-warehouse` | `domain_executor` / `amr_task_executor` | AMR repo build/Git SHA | executor/adapter 产生低频 session heartbeat | `Eligible after stable deployment boundary` |
| Panda MuJoCo execution deployment | `panda-mujoco` | `domain_executor` / `panda_mujoco_executor` | 上游 workspace/build + Git SHA | top-level execution adapter，而不是每个 ROS node | `Eligible after producer contract` |
| Panda Isaac execution deployment | `panda-isaac` | `domain_executor` / `panda_isaac_executor` | adapter + external Isaac build provenance | 独立 Isaac execution adapter | `Eligible after producer contract` |
| Panda PyBullet replay deployment | `panda-pybullet` | `replay_executor` / `panda_pybullet_replay` | 下游 workspace/build + handoff provenance | replay stack adapter；一次 benchmark/replay 形成 Run，不把 risk 当 Task GT | `Eligible after producer contract` |
| Digital Twin bridge deployment | 尚不存在 | `device_bridge` / deployment boundary 未冻结 | micro-ROS Agent + bridge build provenance | 未来受监督 deployment adapter | `Blocked: missing parent Robot + no stable supervision boundary` |

### 明确不单独登记的进程和组件

| 对象 | Classification | 原因 |
|---|---|---|
| AMR Mock WMS API | `Not Registry` | 它是 AMR 域任务权威/外部服务，不是部署到 Robot 的执行 Runtime |
| Nav2 nodes、Gazebo process、AMCL、planner、controller | `Not Registry`（当前） | AMR Runtime 的内部 domain graph；Platform 不做 ROS process inventory |
| `rcr_node_sim`、benchmark、fault fixture | `Not Registry` | 测试/验收进程，不是运营 Runtime |
| micro-ROS Agent、`motor_status_bridge`、`motor_cmd_bridge` 各自 | `Not Registry`（单独） | 当前没有独立 supervision/version/liveness 合同；未来先冻结一个 deployment boundary，再决定是否拆分 |
| teleop ROS nodes、MuJoCo node、Recorder、virtual servo ×7 | `Not Registry`（单独） | 由 Panda execution deployment 统一管理；节点图不是 Registry inventory |
| PolicyRunner、dist monitor、risk engine | `Not Registry`（单独） | 当前属于 replay deployment 内部 harness/observer；risk 不能成为执行真值 |
| Dashboard backend、frontend、HOC server | `Not Registry` | Operations/UI，不是 Robot Runtime |
| Data Lab training/evaluation scripts and batch jobs | `Not Registry` | Data & Validation workload；Checkpoint/Evaluation 是 Artifact/领域对象，不是 Robot Runtime |
| `platformd` | `Not Registry` | 全局 Management Plane 服务，不部署到某台 Robot，也不应自登记成 Robot Runtime |

### Runtime 结论

- 当前最强的 Runtime 候选是 `rcrd`，因为已有独立 binary、systemd unit、版本和监督边界；但没有父 Robot，也没有 Platform producer，所以仍禁止登记。
- 最自然的首个跨仓候选是 AMR task executor：Robot、Task authority 和执行终态都清楚；这只是 D4 候选，不授权现在接入。
- Panda 上游按 MuJoCo/Isaac 分成两个 execution Runtime；下游另有 PyBullet replay Runtime。三个 Runtime 不能共享 session 或 Version。
- Digital Twin 必须先定义父 Robot，并把 micro-ROS Agent/ROS-MQTT bridge 收敛成可监督部署单元，才有 Runtime identity。

## 6. 八仓汇总

| 仓库 | Robot | Device | Runtime | 必须排除 |
|---|---|---|---|---|
| `robot-platform-service` | 无 | 无 | 无 | `platformd`、SQLite DB、curl demo row |
| `robot-control-runtime` | 无；服务对象尚未指定 | Orange Pi 是 Device candidate，但 blocked | `rcrd` candidate，blocked | thread、fd、`vcan0`、CAN frame、node simulator |
| `robot-ops-dashboard` | 无 | 无 | 无 | backend/frontend、card、WebSocket cache、mock evaluation |
| `amr_warehouse_navigation` | Gazebo AMR | sim base/controller、lidar | AMR task executor candidate | map、pose、Nav2 nodes、Mock WMS API |
| `ros2-robot-digital-twin` | 当前无合格 Robot | STM32、ESP32、MPU6050、TB6612、N20，全部 blocked | bridge deployment 尚未冻结 | topic、PWM/PID、firmware task、单电机 bench 作为 Robot |
| `ros2-arm-teleoperation-suite` | MuJoCo Panda、Isaac Panda | 当前不登记仿真内部端点 | 两个 domain executor candidates | ROS node、Recorder、Task GT、Episode |
| `robot-arm-episode-data-lab` | 无 | 无 | 无 | Release、Checkpoint、Evaluation、training batch job |
| `ros2-moveit-pybullet-bridge` | PyBullet Panda | 当前不登记仿真内部端点 | replay executor candidate | PolicyRunner 单体、risk/HOC、legacy iiwa/planar profiles |

## 7. D1 Gate 结果

| Gate | 结果 | 说明 |
|---|---|---|
| 候选分类不依赖 `Device.kind` | `Pass` | v1 `panda-sim/amr-sim` 被定为 disposable mock，不做启发式迁移 |
| Robot / Device / Runtime 无混用 | `Pass` | bench 不升级成 Robot；bridge firmware 不升级成 Runtime；ROS node 不升级成 Registry row |
| 每个可登记 Device 有父 Robot | `Pass with blocked rows` | AMR 两个候选有父 Robot；Digital Twin/Orange Pi 因无父 Robot明确 blocked |
| 每个 Runtime 有真实管理理由 | `Pass with blocked rows` | 只保留 deployment-level candidates；未冻结 supervision/producer 的条目 blocked |
| 当前/目标/Mock 证据分开 | `Pass` | 所有对象仍是 `Not integrated`；demo rows 明确 `Mock-only` |
| 自动数据迁移 | `Not authorized` | 本文件没有产生 SQL、API 或数据写入 |

D1 的“分类”已经完成，但这不表示对象已经进入 Registry。[D2/D3 一致性审阅](MANAGEMENT_PLANE_V2_CONFORMANCE_REVIEW.md) 已冻结五项语义；D2 schema/ID 与 Go verification 已通过，D3 仍为 Fail，因此不能进入 D4 producer 接入。

## 8. 后续设计顺序

1. 保持当前 v1 数据原样，不做迁移。
2. ~~在可用 Go 1.26 环境执行格式化、测试和 vet，关闭 D2 verification。~~ **已完成。** pre-D2 v2 数据只允许显式迁移，不自动猜义。
3. 获得 D3 实施授权后，再落实 trusted SourceContext、Session conflict、Heartbeat admission result 和 Run outcome authority。
4. 只有 D3 implementation Gate 通过后才选一个 producer 进入 D4；首选 AMR task executor，且 Platform 离线不得影响 Nav2/Domain 本地执行。

停止线：在 D3 implementation Gate 通过前，不新增 endpoint、不接 Dashboard、不登记 Digital Twin bench 设备、不为缺失 physical Robot 创建占位资产。

## 9. Evidence index

- Platform：[Phase 1 demo](../scripts/demo.sh)、[本地 v2 demo](../scripts/demo-management-plane.sh)、[拆分合同](ROBOT_DEVICE_RUNTIME_CONTRACT.md)
- Runtime：[README @ 385bc04](https://github.com/inayina/robot-control-runtime/blob/385bc0430b3e1caf8a328f53f31ea019911ec2c0/README.md)、[systemd contract](https://github.com/inayina/robot-control-runtime/blob/385bc0430b3e1caf8a328f53f31ea019911ec2c0/deploy/systemd/README.md)
- Dashboard：[README @ d058f57](https://github.com/inayina/robot-ops-dashboard/blob/d058f5783cd0686c9bd4deb172300e08833a19b2/README.md)、[current scope](https://github.com/inayina/robot-ops-dashboard/blob/d058f5783cd0686c9bd4deb172300e08833a19b2/docs/current_scope.md)
- AMR：[README @ c57ae1c](https://github.com/inayina/amr_warehouse_navigation/blob/c57ae1c401b320118d8cce92db08f2ff6ce87c4d/README.md)、[entry points](https://github.com/inayina/amr_warehouse_navigation/blob/c57ae1c401b320118d8cce92db08f2ff6ce87c4d/setup.py)
- Digital Twin：[README @ 56dd83e](https://github.com/inayina/ros2-robot-digital-twin/blob/56dd83e079325b879b21ca2d2826b265066bdff1/README.md)、[STM32 firmware](https://github.com/inayina/ros2-robot-digital-twin/blob/56dd83e079325b879b21ca2d2826b265066bdff1/firmware/stm32_sensor_node/README.md)、[ESP32 firmware](https://github.com/inayina/ros2-robot-digital-twin/blob/56dd83e079325b879b21ca2d2826b265066bdff1/firmware/esp32_microros_bridge/README.md)、[ROS-MQTT entry points](https://github.com/inayina/ros2-robot-digital-twin/blob/56dd83e079325b879b21ca2d2826b265066bdff1/ros2/robot_mqtt_bridge/setup.py)
- Panda upstream：[README @ f3a7607](https://github.com/inayina/ros2-arm-teleoperation-suite/blob/f3a760774d02aabf6a6bdd2993a53e1738b867b5/README.md)、[scope](https://github.com/inayina/ros2-arm-teleoperation-suite/blob/f3a760774d02aabf6a6bdd2993a53e1738b867b5/docs/PROJECT_SCOPE_AND_ACCEPTANCE.md)
- Panda midstream：[README @ 381cf71](https://github.com/inayina/robot-arm-episode-data-lab/blob/381cf71b5b8942fe0c60ecdc64dab46a00cddabf/README.md)、[inter-repo contract](https://github.com/inayina/robot-arm-episode-data-lab/blob/381cf71b5b8942fe0c60ecdc64dab46a00cddabf/docs/INTER_REPO_CONTRACTS.md)
- Panda downstream：[README @ 985c8da](https://github.com/inayina/ros2-moveit-pybullet-bridge/blob/985c8daa630cf576f450aa63a0d2d12ffff7e0c0/README.md)、[robot profiles](https://github.com/inayina/ros2-moveit-pybullet-bridge/blob/985c8daa630cf576f450aa63a0d2d12ffff7e0c0/pybullet_bridge/config/robot_profiles.yaml)
