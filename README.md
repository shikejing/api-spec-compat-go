# ascheck — 接口描述文档规范校验与向后兼容性差异分析

`ascheck` 在本地对接口描述文档（OpenAPI 2.0 Swagger / OpenAPI 3.x，JSON 或 YAML）做两件事：

1. **规范校验（lint）**：按一组可配置规则检查写法问题，输出带规则标识、`file:line:column` 定位与修复建议的结果。
2. **兼容性差异分析（diff）**：对比任意两个版本，把每一处变更按 `compatible / potentially-breaking / breaking` 分级，区分 **request（请求方向）** 与 **response（响应方向）**，并区分新增、删除、类型收窄/改变、必填性变化、枚举与约束变化等情形。

整个过程只读取本地文件，**不依赖任何外部服务**，不发起网络请求。

## 安装与构建

```bash
go build ./...
go test ./...
go build -o ascheck ./cmd/ascheck
```

唯一的第三方依赖是 `gopkg.in/yaml.v3`（同时负责 YAML 与 JSON 的解析，并保留行列信息）。

## 快速开始

```bash
# 校验一份或多份文档
ascheck lint api/v1/openapi.yaml api/v2/openapi.yaml

# 使用配置文件、临时覆盖单条规则、把机器可读结果写入文件
ascheck lint --config examples/ascheck.config.yaml \
             --set operation-operationId=error \
             --output report.json \
             api/**/*.yaml

# 对比两个版本（可重复 --pair 批量对比）
ascheck diff --pair api/v1/openapi.yaml=api/v2/openapi.yaml

# 也支持位置参数形式对比一对
ascheck diff old.yaml new.yaml

# 列出全部内置规则及默认级别
ascheck rules
```

退出码（便于接入 CI / pre-merge 卡点）：

| 退出码 | 含义 |
| --- | --- |
| `0` | lint 无 error 级问题；diff 无 breaking / potentially-breaking，且无加载失败 |
| `1` | 存在不通过项（lint 的 error/load-error，或 diff 的兼容性风险/加载失败） |
| `2` | 命令行或配置本身有误 |

## 输出格式

两个命令都输出稳定、缩进的 JSON（键与数组顺序确定，可直接被脚本消费）。

`lint`：

```json
{
  "tool": "ascheck",
  "results": [
    {
      "file": "/abs/path/api.yaml",
      "status": "failed",
      "findings": [
        {
          "ruleId": "path-params-defined",
          "severity": "error",
          "message": "path placeholder {id} ... has no matching path parameter",
          "location": {"file": "/abs/path/api.yaml", "line": 12, "column": 7},
          "fix": "add a required parameter named \"id\" with in: path",
          "path": "paths/pets/{id}/get"
        }
      ]
    }
  ],
  "errorCount": 1,
  "warningCount": 0,
  "passed": false
}
```

`diff`：

```json
{
  "tool": "ascheck",
  "reports": [
    {
      "oldFile": "/abs/v1.yaml",
      "newFile": "/abs/v2.yaml",
      "status": "compared",
      "changes": [
        {
          "level": "breaking",
          "direction": "request",
          "kind": "required-added",
          "path": "paths/pets/post/requestBody/content/properties/tag",
          "operation": "POST /pets",
          "description": "required request property added: tag",
          "reason": "existing requests without this property will be rejected",
          "oldLocation": {"file": "...", "line": 10, "column": 6},
          "newLocation": {"file": "...", "line": 12, "column": 8}
        }
      ]
    }
  ],
  "breaking": true
}
```

单份文档/单个对比对解析失败时，结果里对应条目 `status` 为 `load-error` 且带 `error`（同样含行列定位），**其余输入照常处理**，不会中断批量任务。

## 规则语义与配置项

严重级别为四档：`off`（关闭）、`info`、`warning`、`error`。只有 `error` 会使 lint 判定 `failed`（`warning` 时状态为 `warned`，仍为退出码 0）。配置文件示例见 [`examples/ascheck.config.yaml`](examples/ascheck.config.yaml)：

```yaml
rules:
  path-params-defined: error
  operation-operationId: off
```

命令行 `--set RULE=SEVERITY` 可在配置文件之上再覆盖；未在配置里出现的规则沿用默认级别。

| 规则 ID | 默认级别 | 语义 | 修复建议方向 |
| --- | --- | --- | --- |
| `info-title-required` | warning | `info.title` 缺失或为空 | 补充人类可读的 API 名称 |
| `operation-operationId` | warning | 每个 operation 应声明唯一 `operationId` | 添加稳定唯一的 operationId |
| `operation-operationId-unique` | error | `operationId` 不得重复 | 为每个 operation 取不同 id |
| `path-params-defined` | error | 路径中的 `{placeholder}` 必须有对应的 `in: path` 参数 | 补齐对应 path 参数 |
| `path-no-operations` | warning | path item 下没有任何 HTTP 方法 | 增加方法或删除该 path |
| `response-success-defined` | warning | operation 未定义任何 `2xx` 成功响应（HEAD 除外） | 声明至少一个 2xx 响应 |
| `response-description` | info | 响应条目缺少描述文字 | 补充 description |
| `schema-type-valid` | error | schema 使用了无法识别/不支持的 type | 使用标准 JSON Schema 类型 |
| `ref-no-cycles` | info | 检出 `$ref` 递归环（允许存在，但需确认是有意为之） | 确认递归是有意的并文档化 |
| `array-items-defined` | error | `type: array` 缺少 `items` | 声明元素 schema |
| `enum-not-empty` | error | `enum` 为空数组 | 列出取值或删除 enum |
| `enum-unique-values` | warning | `enum` 出现重复取值 | 去重 |
| `object-required-defined` | error | `required` 列出的字段在 `properties` 中不存在 | 补属性或从 required 移除 |
| `property-naming-snake-case` | **off** | 属性名须为 `snake_case`（默认关闭的可选团队约定） | 重命名或在配置中关闭 |
| `operation-deprecated` | info | operation 被标记为 deprecated | 规划替代/下线方案 |

每条 finding 都带 `ruleId`、`severity`、行列 `location`、`message`、可操作的 `fix` 与稳定的 JSON-pointer 风格 `path`。同一输入多次校验结果逐条一致（按 file → line → column → ruleId → message 排序）。

## 兼容性分级语义

差异分析的核心原则是**保守判定**：任何无法静态证明安全的变更，都不会被判定为 `compatible`，而是 `potentially-breaking` 并给出理由；明确会拒绝旧请求或让旧客户端读不到承诺数据的判为 `breaking`。

| 变更 | request（客户端→服务端） | response（服务端→客户端） |
| --- | --- | --- |
| 新增 operation / 新增可选参数 / 可选请求字段 | compatible | compatible（新增可选响应字段通常被容忍忽略） |
| 新增**必填**参数/请求字段 | **breaking** | potentially-breaking（承诺增强但无法静态验证旧服务） |
| 删除 operation / 参数 / 请求字段 | **breaking**（可选参数删除同样保守处理） | **breaking**（响应字段删除：客户端读不到） |
| 响应字段从必填变可选（不再承诺存在） | — | **breaking** |
| 请求字段从必填变可选 | compatible | — |
| 枚举**收窄**（删值） | **breaking**（旧合法值被拒） | potentially-breaking（穷举处理/硬编码的客户端可能出错） |
| 枚举**放宽**（增值） | compatible（旧值仍合法） | potentially-breaking（可能下发客户端没见过的值，保守处理） |
| 基础类型改变（string→integer 等） | **breaking** | **breaking** |
| integer↔number | potentially-breaking（精度/范围无法静态保证） | potentially-breaking |
| 新增/收紧 `minimum/maximum/length/pattern/items` 等约束 | **breaking**（旧值可能被拒） | potentially-breaking（已部署服务无法保证新约束） |
| 放宽约束 | compatible | potentially-breaking（可能下发超出旧范围的值） |
| 变为 `nullable` | potentially-breaking（引入 null，保守处理） | potentially-breaking |
| 不再 `nullable` | **breaking** | **breaking** |
| `oneOf/anyOf` 联合类型替换 | potentially-breaking：分支取值域重叠无法静态证明，绝不自动判兼容 | 同左 |
| `not` / `allOf` 等组合构造变化 | potentially-breaking：组合语义无法完全静态求值 | 同左 |
| 无类型（`any`）与具体类型互转 | potentially-breaking：无静态可证明的保证 | potentially-breaking |
| operation 标记 deprecated | potentially-breaking（警告） | potentially-breaking |
| 新增响应状态码 | compatible（客户端可忽略未处理的变体） | — |
| 删除响应状态码 | **breaking** | **breaking** |

每个 Change 都带 `direction`、`kind`、分级 `level`、稳定 `path`、人读 `description`，以及对保守结论的 `reason` 说明；`oldLocation/newLocation` 指回两侧源码位置。

## 引用解析、循环与性能

- 支持文件内与跨文件 `$ref`（相对路径相对当前文件解析），JSON Pointer 片段按 RFC 6901 解码（`~1`、`~0`）。
- 引用解析失败（文件不存在、指针无目标、YAML/JSON 语法错误）都会返回带 `file:line:column` 的错误，定位指向具体的 `$ref` 节点或语法位置；不支持远程 `http(s)://` 引用并明确报错。
- 引用链上的环会被检测，递归在回边处安全终止（产出 `ref` 占位节点），不会死循环或崩溃；`ref-no-cycles` 规则会显式提示。
- 性能与确定性：
  - 文件内容、解析后的 AST、以及每个被引用定义构建出的 schema 都有缓存；**同一引用无论出现多少次，解析/构建只发生一次**（同一定义的多个使用点共享同一 schema 实例）。
  - 加载同一文件的并发请求通过 singleflight 合并。
  - 输出在所有层级都做了确定性排序，**结果与文档处理顺序、worker 数量无关**。
  - 批量处理使用有界 worker 池（`--workers`，默认 NumCPU），单份失败只隔离该条目。

## 作为 Go 库使用

```go
l := spec.NewLoader()
doc, err := l.LoadFile("api.yaml")           // 加载 + 规范化 + 引用解析

eng := rules.NewEngine(rules.DefaultConfig()) // 或 rules.LoadConfig(path)
findings := eng.Lint(doc)

rep := compat.NewComparator().CompareFiles(l, "v1.yaml", "v2.yaml")
rep.HasBreaking()

runner := batch.NewRunner(l, eng, 8)
summary := runner.LintAll([]string{"a.yaml", "b.yaml"})
```

## 包结构

```
internal/spec    文档加载、YAML/JSON 解析、$ref 解析与循环保护、规范化模型
internal/rules   规则接口、严重级别/配置、内置规则集与确定性排序
internal/compat  操作级与 schema 级差异分析、保守的兼容性分级
internal/batch   有界并发批量处理、错误隔离与汇总
internal/report  稳定的 JSON 输出
cmd/ascheck      lint / diff / rules 子命令与退出码
```
