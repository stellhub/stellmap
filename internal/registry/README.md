# internal/registry

## 这个包是做什么的

`internal/registry` 承载注册中心的领域模型和规则，负责描述“实例是什么、如何注册、如何查询候选集、如何判断是否过期、如何对外发布变化事件”。

它不处理 HTTP、gRPC、Raft 启动或存储装配，而是专注在注册中心本身的业务语义。

## 核心逻辑

这个包的核心逻辑主要有三块：

1. 实例模型与注册输入规范化
   `model.go` 定义了 `Endpoint`、`RegisterInput`、`Value`、`Query` 等核心结构，并负责：
   - 清理 namespace/service/instanceId 等字段的空白
   - 校验端点协议、地址、端口、权重
   - 兜底默认 TTL 和默认端点权重
   - 构造最终落到状态机里的 `Value`

2. 实例查询与 watch 条件解析
   `ParseQuery` 负责把查询参数转换成领域层的 `Query`，并进一步复用 `selector.go` 里的标签过滤语法，供实例查询和事件 watch 共用。

3. 过期实例判断与分批清理游标
   - `IsAlive` 根据最后心跳时间和 TTL 判断实例是否仍然存活
   - `CleanupCursor` 负责记录下一轮清理从哪个 key 继续扫，避免每次都从头全量扫描

4. 实例变化事件模型
   `watch.go` 定义了注册中心内部的 watch 事件和 hub，统一表达实例快照、upsert、delete 等变化。

## 提供了哪些能力

- 定义注册中心实例、端点、查询条件等领域对象
- 规范化注册输入并做基础校验
- 生成注册实例对应的状态机 key 和服务前缀
- 解析 `selector` / `label` 过滤条件
- 判断实例是否过期
- 支持按批次推进的过期清理扫描游标
- 定义实例变化事件模型，供 SSE watch 等上层能力复用

## selector 能力说明

这个包内置了一套 Kubernetes 风格子集的标签选择器语法，支持：

- `key`
- `!key`
- `key=value`
- `key!=value`
- `key in (v1,v2)`
- `key notin (v1,v2)`

同时保留了旧的 `label=key=value` 兼容参数，便于老调用方平滑过渡。

## 关键文件

- `model.go`
  注册中心领域对象、注册输入规范化、查询解析、TTL 判断
- `selector.go`
  标签选择器语法解析、错误提示、匹配逻辑
- `cleanup.go`
  后台过期清理扫描游标
- `watch.go`
  注册中心实例变化事件模型与 watch hub
- `selector_test.go`
  selector 错误提示回归测试
- `cleanup_test.go`
  清理游标推进与重置测试

## 包边界

这个包只负责“注册中心规则”，不负责：

- HTTP 参数编解码
- Raft 日志提交
- 状态机扫描与删除执行
- 后台任务调度

这些职责分别由上层应用层、transport 层和 storage 层完成。
