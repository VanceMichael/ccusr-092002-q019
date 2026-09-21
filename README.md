# 高坝鱼类过坝链路核验

叶巴滩电站鱼类通过**集鱼槽收集 → 站点分拣 → 155 米轨道提升 → 无人车陆运 → 运鱼船船运 → 放流点**越过高坝。本服务对一条鱼批次在每一段的**物种、数量、箱体交接、设备故障**逐段登记并执行数量守恒核验：

- 每段交接必须登记运输箱清单，箱体数量合计与交接总数一致；
- 接收清点出现短少时，交接单挂 `DISPUTED`、批次变 `BLOCKED`，**差额必须有经确认的死亡记录或重新分拣记录全额销账后，批次才能继续流转**；
- 死亡/重分拣都要先登记（`PENDING`）再由监督人员确认（`CONFIRMED`），待确认记录同样阻止流转；驳回（`REJECTED`）不扣减账面；
- 设备故障即使零死亡也留痕，死亡记录可关联故障；
- 增殖鱼苗（`HATCHERY`）的耳石/荧光标记与放流后回捕调查关联；天然过坝野生鱼（`WILD`）即使回捕也**不计入增殖放流成效**；
- 管理部门通过一个追溯接口看清每段交接的守恒结果、放流地点与数月后的监测证据。

技术形态：HTTP + JSON 接口、SQLite 本地文件（纯 Go 驱动 `modernc.org/sqlite`，CGO 关闭即可构建），迁移在服务启动时自动执行。

## 本地开发

```bash
make migrate   # 可选：预热数据库（服务启动也会自动迁移）
make test      # 运行自动化检查
make run       # 启动服务，默认 :8080 / data/app.sqlite3
```

运行参数：`PORT`（监听端口，默认 8080）、`DATABASE_PATH`（SQLite 文件，默认 data/app.sqlite3）。
`docker compose up --build` 启动隔离容器，`APP_PORT` 可调整宿主机端口。

`fixtures/example.json` 保存不含真实身份的交换示例，`contracts/entities.json` 记录字段约定，`docs/domain.md` 介绍来源与范围。

## 标准环节

`GET /v1/stages` 返回顺序：

| code | 环节 |
|---|---|
| COLLECTION | 集鱼槽收集 |
| SORTING | 站点分拣 |
| RAIL_LIFT | 轨道提升（155 米） |
| LAND_TRANSPORT | 无人车陆运 |
| VESSEL_TRANSPORT | 运鱼船船运 |
| RELEASE_SITE | 放流点 |

交接只能沿相邻环节依次进行，不允许跳段。

## API 一览

所有请求/响应均为 JSON；时间字段使用带时区偏移的 ISO 8601（如 `2026-09-20T08:30:00+08:00`）。
错误码：`400` JSON 非法/含未知字段；`404` 记录不存在；`409` 当前状态不允许操作；`422` 违反守恒或登记规则。

### 批次

- `POST /v1/lots` 登记批次：`lot_ref`、`species_code`、`origin`（WILD/HATCHERY，默认 WILD）、`count`。
- `GET /v1/lots?origin=HATCHERY` 批次列表（可按来源过滤）。
- `GET /v1/lots/{lot_ref}` 批次当前状态与账面。
- `GET /v1/lots/{lot_ref}/trace` **全链路追溯视图**（见下）。

### 逐段交接

- `POST /v1/handovers` 发出：`lot_ref`、`from_stage`、`to_stage`、`sent_count`、`containers[]`（每箱 `container_ref`+`sent_count`，合计必须等于 `sent_count`）。整批交接，`sent_count` 必须等于批次账面存活数。
- `POST /v1/handovers/{id}/receive` 接收清点：可按箱给 `containers[].received_count`（允许某箱为 0），也可只给整单 `received_count`。如数 → `CONFIRMED`；短少 → `DISPUTED`。
- `GET /v1/handovers/{id}` 交接单与箱体明细。
- `GET /v1/handovers/{id}/reconciliation` 该段销账结果：`shortage`、`losses_confirmed`、`resorts_confirmed`、`pending_losses`、`pending_resorts`、`cleared`。

### 死亡与重新分拣（差额销账）

- `POST /v1/losses`：`lot_ref`、`count`、`cause`；销交接短少带 `handover_id`，站点存活期死亡带 `stage_code`；可附 `fault_id`。
- `POST /v1/losses/{id}/confirm` / `POST /v1/losses/{id}/reject` 确认或驳回。
- `POST /v1/resorts` 重新分拣：`from_lot_ref`、`count`、`reason`；目标给 `to_lot_ref`，或留空由服务按 `to_species_code` 新建批次；销交接短少带 `handover_id`，站点重分拣带 `stage_code`。不允许改变 WILD/HATCHERY 来源。
- `POST /v1/resorts/{id}/confirm` / `POST /v1/resorts/{id}/reject`。

### 设备故障

- `POST /v1/faults`：`equipment_ref`、`stage_code`、`fault_type`、`description`、`lot_refs[]`。
- `POST /v1/faults/{id}/resolve` 排除故障。
- `GET /v1/faults?lot_ref=...` 故障记录。

### 放流、标记与放流后监测

- `POST /v1/releases` 放流：`lot_ref`、`count`（必须等于账面存活数）、`waterbody`、`site_name`、经纬度、`released_at`。一批一次。
- `POST /v1/marks` 标记：`lot_ref`、`mark_type`（OTOLITH/FLUORESCENT/PIT/OTHER）、`mark_code`、`marked_count`、`marked_at`。
- `POST /v1/surveys` 回捕调查（可在放流数月后）：`survey_date`、`waterbody`、`site_name`、调查方法等。
- `POST /v1/surveys/{id}/recaptures` 登记回捕：带 `mark_code` 且命中标记 → `MATCHED` 并回填 `matched_lot_ref`；无标记或编码未知 → `UNMATCHED`。
- `GET /v1/recaptures?lot_ref=...` 回捕记录（含调查日期/水体）。

## 追溯视图（管理部门查询）

`GET /v1/lots/{lot_ref}/trace` 返回：

- `balance`：账面恒等式 `存活 available + 确认死亡 losses_confirmed + 已放流 released = 初登 initial + 重分拣入 resorted_in − 重分拣出 resorted_out`；
- `stages[]`：每段交接的发出/接收/短少、销账构成与结果（`IN_TRANSIT` / `BALANCED` / `BLOCKED`），附挂在该段的死亡与重分拣记录；
- `standalone_losses`、`resorts`、`faults`：站点死亡、重分拣、设备故障留痕；
- `release`：放流水体、地点名称与坐标；
- `marks`、`recaptures`：增殖标记与放流后回捕调查证据（调查日期、水体、江段）；
- `effectiveness`：`eligible` 仅当来源为 HATCHERY，且 `matched_recapture_count` 只统计标记命中的回捕——野生鱼与无标记个体不进入成效口径；
- `conservation_ok` 与 `open_issues`：全链路是否闭合及中文未决问题清单。

## 典型短少处理流程

```text
分拣站发出 120（2 箱各 60）
提升站接收 118（60 + 58）         → 交接 DISPUTED，批次 BLOCKED
登记设备故障（提升机停车）
登记 2 尾死亡并关联该交接单/故障   → PENDING，仍不可流转
监督人员确认死亡                  → CONFIRMED，交接自动 CONFIRMED，批次推进
剩余 118 继续陆运、船运、放流点
放流 118，追溯视图 conservation_ok=true
```
