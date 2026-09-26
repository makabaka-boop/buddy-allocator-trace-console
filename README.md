# Buddy Allocator Replay

伙伴（binary buddy）内存分配器的**批量回放**系统：Go HTTP 服务负责回放，Svelte
页面展示分裂树与地址条，Docker Compose 分别运行 `allocator` 与 `desk`。

## 规则

- 总容量为 2^k 字节，k ∈ [4, 16]（16 B ～ 65536 B）。
- 每批至多 300 个事件，`eventId` 全局唯一。
- `allocate`：带全局唯一 `allocId` 与 1～容量 的请求大小；请求向上取整到能容纳
  它的最小阶（最小适配），同阶选**最低地址**空块，分裂时沿**低地址半块**一路切下。
- `free`：引用一个当前存活的 `allocId`；按伙伴地址逐级合并——只有伙伴**真正空闲
  且同阶**时才合并。
- OOM（没有任何能容纳请求的空块，例如总空闲够但全是碎片）是**有效事件**：快照标记
  `oom:true`，分配器状态与上一步完全一致。
- 结构错误（未知/已释放的 `allocId`、重复 id、大小越界、未知事件类型、容量非法…）
  导致**整批拒绝**（HTTP 400），不产生任何状态。

## 响应内容（逐事件快照）

每个事件返回：本事件块 `block`、当前全部 `allocated` 块、按阶索引的 `free` 空块、
`internalFragmentation`（块大小 − 请求大小之和）、`totalFree`、`largestFree` 与
`externalFragmentation = totalFree − largestFree`（即总空闲减去最大空块，捕捉
"总空闲够但分不出来" 的碎片）。页面逐步回放的就是这同一份响应。

## 目录

```
allocator/          Go 服务 + buddy 引擎
  buddy/            引擎、校验与参考模型测试（字节占用核对不重叠/守恒/裁决/合并）
  main.go           POST /api/replay, GET /healthz
desk/               Svelte + Vite 页面
  src/lib/tree.js   快照 -> 分裂树 / 地址条分段
  src/App.svelte    逐步回放、地址条、各阶空块、碎片指标
  tests/e2e/        Playwright：分配—释放—重用主流程 + OOM + 整批拒绝
docker-compose.yml  allocator / desk 两个服务
```

## 本地运行

```bash
# 后端 :8080
cd allocator && go run .

# 前端 :5173（/api 代理到 ALLOCATOR_URL，默认 http://localhost:8080）
cd desk && npm install && npm run dev
```

```bash
# Docker
docker compose up --build
# 页面 http://localhost:5173 ，desk 容器内 nginx 把 /api 反代给 allocator
```

## 测试

```bash
cd allocator && go test -race ./...   # 随机 60 批对照独立树+字节网格参考模型
cd desk && npx playwright test        # 需要 allocator 与 desk 已在运行
```

## API 示例

请求：

```json
{
  "capacity": 16,
  "events": [
    {"eventId": "e1", "type": "allocate", "allocId": "a", "size": 9},
    {"eventId": "e2", "type": "allocate", "allocId": "b", "size": 1},
    {"eventId": "e3", "type": "free", "allocId": "a"},
    {"eventId": "e4", "type": "allocate", "allocId": "c", "size": 8}
  ]
}
```

- `e1`：9 B → 阶 4（16 B）整块 `[0,16)`，内部碎片 7 B。
- `e2`：总空闲为 0 → **OOM**，状态不变。
- `e3`：释放 `a`，合并回整块 `[0,16)`。
- `e4`：8 B → 阶 3，最低地址 `[0,8)`，`[8,16)` 为空伙伴。
