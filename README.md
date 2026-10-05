# api-spec-compat-go

面向接口描述文档（OpenAPI 3.x / Swagger 2.0）的**规范校验**与**向后兼容性差异分析**工具。
全部处理在本地完成，不依赖任何外部服务。

## 构建与测试

```sh
go build ./cmd/speccheck
go test ./...
```

## 使用

```sh
# 规范校验（可一次传多份文档，单份失败不影响其余）
speccheck lint [--config rules.yaml] [--format json|text] [--jobs N] file...

# 两个版本的兼容性差异分析
speccheck diff [--format json|text] [--fail-on breaking|warning] OLD.yaml NEW.yaml

# 列出全部内置规则
speccheck rules
```

退出状态：

| 退出码 | 含义 |
|---|---|
| 0 | 全部通过 |
| 1 | 存在加载失败、error 级别校验发现，或破坏性变更（`--fail-on=warning` 时含 warning 级变更） |
| 2 | 用法错误（参数、配置文件非法等） |

`lint` 的 JSON 输出是可机器解析的汇总：`results[]`（按文件名排序，含每份文档的
`findings[]` 或 `error`）、`errorCount`、`warnCount`、`loadErrors`、`ok`。
输出与文档处理顺序、并发度无关：相同输入多次运行结果逐条一致。

## 文档加载与引用解析

- 支持 OpenAPI 3.x（`openapi: 3.x.y`）与 Swagger 2.0（`swagger: "2.0"`），统一规范化为同一模型。
- `$ref` 完整解析到实际定义，支持跨文件相对路径引用（`./common.yaml#/components/schemas/X`）；
  不支持远程（`http://`）引用，遇到会报带位置的错误。
- 循环引用（包括跨文件循环）以占位节点表示，不会死循环或崩溃。
- 相同引用目标只解析一次（按 `文件#指针` 记忆化），解析开销不随引用出现次数线性增长；
  同一目标的所有引用共享同一个 schema 实例。
- 解析失败的错误携带 `文件:行:列` 定位。
- 对象型 `allOf` 在规范化阶段合并进父 schema（properties/required 取并集）。

## 校验规则

每条发现包含：规则标识 `ruleId`、严重级别、定位信息（文件/行/列）、消息与修复建议。

| 规则 ID | 默认级别 | 语义 |
|---|---|---|
| `operation-id-required` | error | 每个操作必须声明 operationId |
| `operation-id-unique` | error | operationId 全文唯一 |
| `path-no-trailing-slash` | warn | 路径不得以 `/` 结尾 |
| `path-param-declared` | error | 路径模板 `{x}` 与 path 参数声明必须一一对应 |
| `response-description-required` | warn | 每个响应必须有非空 description |
| `success-response-schema` | warn | 2xx（除 204）响应应声明 schema |
| `schema-type-required` | warn | schema 必须声明 type、组合（oneOf/anyOf/allOf）或 enum |
| `enum-values-valid` | error | enum 非空且无重复值 |
| `param-name-case` | warn | query/cookie/path 参数用 lowerCamelCase，header 用 kebab-case |

### 配置

通过 `--config` 指定 YAML 文件，逐条覆盖规则的开关与严重级别：

```yaml
rules:
  operation-id-required: error   # off | warn | error
  path-no-trailing-slash: off
  param-name-case: warn
```

未出现在配置中的规则使用默认级别；`off` 表示关闭。未知规则 ID 或非法级别会在启动时报错。

## 兼容性分级语义

`diff` 把每个变更分级为：

- **breaking**：确定破坏向后兼容；
- **warning**：无法静态判定，按保守原则处理（绝不因未知构造判为兼容）；
- **info**：向后兼容。

并区分方向：**request**（判断旧客户端的请求是否仍被接受）与 **response**
（判断旧客户端是否仍能消费新版本可能产出的所有响应）。

| 变更 | request 方向 | response 方向 |
|---|---|---|
| 路径/操作 删除 / 新增 | breaking / info（方向为 none） | 同左 |
| 参数 删除 | 必填→breaking；可选→warning | — |
| 参数 新增 | 必填→breaking；可选→info | — |
| 参数/属性 变为必填 | breaking | info（响应属性变为保证存在） |
| 参数/属性 变为可选 | info | warning（客户端可能依赖其存在） |
| 响应状态码 删除 / 新增 | — | breaking / info |
| 响应属性 删除 / 新增 | — | breaking / info |
| 请求属性 删除 | additionalProperties=false→breaking；否则 warning | — |
| 类型变化（如 string→integer） | breaking | breaking |
| integer↔number | 变宽 info / 变窄 breaking | 变宽 breaking / 变窄 info |
| nullable 新增 / 移除 | info / breaking | breaking / info |
| int32↔int64 | 变宽 info / 变窄 breaking | 变宽 breaking / 变窄 info |
| 其他 format 变化 | warning（无法静态判定） | warning |
| enum 收窄（新⊂旧） | breaking | info |
| enum 变宽（新⊃旧） | info | breaking |
| enum 新增 / 移除约束 | breaking / info | info / breaking |
| enum 无子集关系的变化 | warning（保守判定） | warning |
| oneOf/anyOf/未合并组合变化 | warning（保守判定，附理由） | warning |
| additionalProperties 变为 false | breaking | warning |

所有无法静态判定的情形（联合类型替换、format 变化、无子集关系的枚举变化等）
一律输出 warning 并说明理由，不会被判定为兼容。

## 批量处理与性能

- `lint` 接受任意多份文档，使用 worker 池并发处理（`--jobs`，默认 NumCPU）。
- 单份文档加载失败或内部 panic 不会中断其余文档，错误记录在该文档的结果中。
- 引用解析按目标记忆化；实测 2000 处引用 + 200 层引用链的文档加载约 60ms。
- 汇总结果按文件名排序，finding 按 (文件, 行, 列, 规则, 消息) 排序，
  与处理顺序和并发度无关。

## 代码结构

```
cmd/speccheck/     CLI 入口
internal/spec/     规范化模型（版本无关）
internal/loader/   加载、$ref 解析（跨文件/循环安全、记忆化）、规范化
internal/lint/     规则引擎与内置规则
internal/diff/     兼容性差异分析
internal/batch/    并发批量处理与机器可读汇总
```
