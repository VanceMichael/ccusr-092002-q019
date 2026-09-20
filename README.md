# 高坝鱼类过坝链路核验

叶巴滩电站用集鱼槽、155 米轨道提升、无人车和运鱼船帮助本土鱼类越过高坝。本服务提供贯穿
**分拣 → 155米轨道提升 → 陆运 → 船运 → 放流** 的批次核验后端：逐段登记物种、数量与运输箱交接，
短数必须有经确认的死亡或重新分拣记录才能继续流转；增殖鱼苗的耳石/荧光标记与数月后的回捕调查关联，
天然野生鱼不会被误计入放流成效。

技术栈：Go 1.24 + 标准库 `net/http` + SQLite（纯 Go 驱动 `modernc.org/sqlite`，可无 CGO 静态构建）。
运行参数 `PORT`（默认 8080）、`DATABASE_PATH`（默认 `data/app.sqlite3`）。迁移 SQL 已嵌入二进制，
服务启动自动应用。`fixtures/example.json` 为脱敏交换示例，`contracts/entities.json` 记录字段与枚举约定，
`docs/domain.md` 介绍领域规则。

## 本地开发

```sh
make test      # 运行自动化检查（含端到端故事测试）
make run       # 启动服务（自动迁移）
make migrate   # 仅执行数据库迁移后退出
make vet       # 静态检查
docker compose up --build   # 启动隔离容器，APP_PORT 可调整宿主机端口
```

## 接口一览（/v1）

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/v1/lots` | 登记鱼批（`origin` 必填 `WILD`/`HATCHERY`；重分拣新批次可指定 `start_stage`） |
| GET | `/v1/lots` | 批次列表，支持 `?origin=`、`?species=` 过滤 |
| GET | `/v1/lots/{lot}` | 批次档案 |
| GET | `/v1/lots/{lot}/trace` | **管理查询：全链路守恒结果、放流点、监测证据** |
| PUT | `/v1/lots/{lot}/handoffs/{stage}` | 登记某环节发出与逐箱清单（箱量合计须等于守恒应发数） |
| POST | `/v1/lots/{lot}/handoffs/{stage}/receive` | 接收方逐箱清点（短数即 BLOCKED） |
| GET | `/v1/lots/{lot}/handoffs/{stage}` | 单段交接的守恒结果 |
| POST | `/v1/lots/{lot}/handoffs/{stage}/losses` | 上报死亡（DEATH）或重新分拣（RESORT）凭证 |
| POST | `/v1/loss-events/{id}/confirm` | 确认/驳回凭证（`approve`、`confirmed_by`） |
| GET | `/v1/lots/{lot}/losses` | 批次的全部凭证 |
| POST | `/v1/lots/{lot}/release` | 配平后登记放流地点并完成放流 |
| POST | `/v1/lots/{lot}/failures` | 登记与批次相关的设备故障（不能抵减短数） |
| GET | `/v1/lots/{lot}/failures` | 批次相关故障列表 |
| POST | `/v1/failures` | 只登记设备侧故障（不绑定批次） |
| POST | `/v1/lots/{lot}/marks` | 增殖批次登记耳石/荧光标记（野生批次 400） |
| GET | `/v1/lots/{lot}/marks` | 批次标记列表 |
| POST | `/v1/surveys` | 登记放流后的回捕调查 |
| GET | `/v1/surveys/{survey}` | 调查档案 |
| POST | `/v1/surveys/{survey}/recaptures` | 登记回捕检出（自动定性 CREDITED/WILD/UNMATCHED_MARK） |
| GET | `/v1/surveys/{survey}/recaptures` | 调查的全部检出 |

环节代码：`SORTING`、`HOIST_155M`、`LAND_TRANSPORT`、`VESSEL_TRANSPORT`、`RELEASE`。
错误响应统一为 `{"code":..., "message":...}`，状态码：400 参数不合法、404 不存在、409 状态冲突
（跳段、上一段未配平、凭证未确认/超额、重复登记等）。

## 端到端示例

```sh
curl -s localhost:8080/v1/lots -d '{"lot_code":"LOT-A","species_code":"SCHIZOTHORAX",
  "origin":"HATCHERY","initial_count":120}'

curl -s -X PUT localhost:8080/v1/lots/LOT-A/handoffs/HOIST_155M -d '{
  "containers":[{"container_ref":"BOX-1","expected_count":60},
                {"container_ref":"BOX-2","expected_count":60}]}'

# 实收短 2 尾后，交接变 BLOCKED，陆运段登记被 409 拒绝；
# 上报 DEATH 凭证并经 /v1/loss-events/{id}/confirm 确认后，交接自动配平。
```
