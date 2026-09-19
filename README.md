# 高坝鱼类过坝链路核验

鱼类通过集鱼槽、轨道提升、无人车和船运越过高坝，运输箱交接数量与增殖鱼苗放流数据分开计量。

本服务采用 HTTP 接口和 SQLite 本地文件。运行参数 `PORT` 指定监听端口，`DATABASE_PATH` 指定数据文件；`fixtures/example.json` 保存不含真实身份的交换示例，`contracts/entities.json` 记录字段约定，`docs/domain.md` 介绍来源与范围。

## 本地开发

`make migrate` 初始化数据文件，`make test` 运行现有自动化检查，`make run` 启动服务。`docker compose up --build` 可以启动隔离容器，`APP_PORT` 可调整宿主机端口。
