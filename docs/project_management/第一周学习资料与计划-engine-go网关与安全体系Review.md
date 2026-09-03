# 第一周学习资料与计划：engine-go 网关与安全体系 Review

> **周目标**：完成 engine-go 核心模块的人工 Review，建立对网关流量治理、安全认证体系与可观测性实现的全局认知。
>
> **审查范围**：`engine-go/internal/gateway/`、`engine-go/internal/security/`、`engine-go/internal/observability/`、`pkg/gateway/`、`pkg/auth/`、`pkg/tlsutil/`、`pkg/middleware/`、`pkg/observability/`、`pkg/circuitbreaker/`。
>
> **代码总量**：约 30 个 Go 源文件，~4,500 行实现代码 + ~1,500 行测试代码。

---

## 目录

- [第 1 章：前置知识准备](#第-1-章前置知识准备)
  - [P0：Go 并发原语速览](#p0go-并发原语速览)
  - [P1：HTTP 反向代理原理](#p1http-反向代理原理)
  - [P2：gRPC 流式通信模型](#p2grpc-流式通信模型)
  - [P3：mTLS 双向认证基础](#p3mtls-双向认证基础)
  - [P4：Prometheus 指标类型](#p4prometheus-指标类型)
  - [P5：令牌桶限流算法](#p5令牌桶限流算法)
- [第 2 章：Day 1-2 网关与流量治理 Review](#第-2-章day-1-2-网关与流量治理-review)
- [第 3 章：Day 3-4 安全体系 Review](#第-3-章day-3-4-安全体系-reviewmtls认证权限)
  - [3.1 审查文件清单](#31-审查文件清单)
  - [3.2 核心知识点详解](#32-核心知识点详解)
  - [3.3 Day 3-4 Review 方法](#33-day-3-4-review-方法)
  - [3.4 安全体系纵深分析](#34-安全体系纵深分析)
  - [3.5 接口权限控制漏洞修复案例](#35-接口权限控制漏洞修复案例sec-09--sec-13)
- [第 4 章：三级等保/密评合规增强](#第-4-章三级等保密评合规增强)
  - [4.1 G-03 登录失败处理与账号锁定](#41-g-03-登录失败处理与账号锁定)
  - [4.2 G-04 密码策略强化](#42-g-04-密码策略强化)
  - [4.3 G-05 JWT 令牌吊销](#43-g-05-jwt-令牌吊销)
  - [4.4 G-11 TOTP 双因素认证](#44-g-11-totp-双因素认证)
  - [4.5 G-12 WAF Web 攻击防护](#45-g-12-waf-web-攻击防护)
  - [4.6 G-14 API Key 过期与自动轮换](#46-g-14-api-key-过期与自动轮换)
  - [4.7 G-02 可信代理与源地址校验](#47-g-02-可信代理与源地址校验)
- [第 5 章：Day 5 可观测性 Review](#第-5-章day-5-可观测性-review日志指标追踪)
- [第 6 章：代码走读指南](#第-6-章代码走读指南)
- [第 7 章：常见问题与排查指南](#第-7-章常见问题与排查指南)
- [第 8 章：术语表](#第-8-章术语表)
- [第 9 章：Engine-go 启动流程与配置加载链路深度分析](#第-9-章engine-go-启动流程与配置加载链路深度分析)
- [第 10 章：gRPC 透明代理完整代码走读](#第-10-章grpc-透明代理完整代码走读)
- [第 11 章：网关部署拓扑与运维指南](#第-11-章网关部署拓扑与运维指南)
- [第 12 章：Review 检查清单详细版](#第-12-章review-检查清单详细版)
- [附录 A：关键环境变量速查表](#附录-a关键环境变量速查表)
- [附录 B：核心数据结构关系图](#附录-b核心数据结构关系图)
- [附录 C：安全认证链路全景图](#附录-c安全认证链路全景图)
- [附录 D：推荐阅读与延伸阅读](#附录-d推荐阅读与延伸阅读)


---

## 第 1 章：前置知识准备

在开始 Review 之前，需要掌握以下基础概念。这些知识是理解网关与安全体系代码的前提。

### P0：Go 并发原语速览

本项目的网关与限流模块大量使用了 Go 的并发原语，理解它们是读懂代码的基础：

| 原语 | 用途 | 本项目使用场景 |
|---|---|---|
| `sync.Mutex` / `sync.RWMutex` | 互斥锁 / 读写锁 | 熔断器状态保护、白名单重载 |
| `sync/atomic` (`Int32`, `Int64`) | 无锁原子操作 | InFlight 计数、RoundRobin 索引、SWRR 当前权重 |
| `sync.Pool` | 对象池，GC 时清空 | BufferPool 32KB 缓冲区复用 |
| `sync.Once` | 确保函数仅执行一次 | 反向代理惰性创建、追踪器初始化 |
| `chan struct{}` | 信号通道 | 限流器 GC 停机信号、并发信号量 |
| CAS 循环 (`CompareAndSwap`) | 无锁原子递减防负数 | `DecrementInFlight()` |

以下是每个并发原语都附带**完整可运行代码片段**的详细解释：

---

#### 1. `sync.Mutex` / `sync.RWMutex`

**`sync.Mutex`** 用于**完全互斥**：同一时刻只有一个 goroutine 能进入临界区。  
**`sync.RWMutex`** 用于**读多写少**：读操作可并发，写操作独占。
- `sync.Mutex`：最基本的互斥锁。当一个 goroutine 调用 Lock() 后，其他所有尝试 Lock() 的 goroutine 都会进入阻塞等待，直到持有者调用 Unlock()。适用于读写频率相当，或写操作占主导的场景。 
- `sync.RWMutex`：读写分离锁。允许多个 goroutine 同时持有读锁（RLock()），但写锁（Lock()）会独占，且写锁会阻塞新的读锁。适用于"读多写少"的场景，如熔断器状态查询（高频读）与状态切换（低频写）。 

本项目注意：白名单重载时，使用写锁确保重载过程中不会有 goroutine 读到半量数据；熔断器状态查询使用读锁，保证并发查询不互斥。 
```go
package main

import (
    "fmt"
    "sync"
    "time"
)

// CircuitBreaker 熔断器状态机
type CircuitBreaker struct {
    mu     sync.RWMutex        // 保护 state 的读写锁
    state  string              // "closed"(通路) / "open"(熔断) / "half-open"(半开)
    counts int                 // 连续失败计数
}

// GetState 高频调用：使用 RLock，允许多个 goroutine 并发读
func (cb *CircuitBreaker) GetState() string {
    cb.mu.RLock()         // 读锁：多个 goroutine 可同时持有
    defer cb.mu.RUnlock()
    return cb.state
}

// Trip 低频调用：使用 Lock，独占式修改状态
func (cb *CircuitBreaker) Trip() {
    cb.mu.Lock()          // 写锁：阻塞所有读锁和写锁
    defer cb.mu.Unlock()
    cb.state = "open"
    cb.counts = 0
    fmt.Println("熔断器已打开")
}

// Reset 恢复通路
func (cb *CircuitBreaker) Reset() {
    cb.mu.Lock()
    defer cb.mu.Unlock()
    cb.state = "closed"
    cb.counts = 0
}

// WhiteList 白名单配置，需要全量重载
type WhiteList struct {
    mu    sync.RWMutex
    rules map[string]bool  // 如: {"192.168.1.1": true}
}

// Reload 全量替换：必须加写锁，防止读取方读到半量数据
func (wl *WhiteList) Reload(newRules map[string]bool) {
    wl.mu.Lock()
    defer wl.mu.Unlock()
    
    // 直接替换 map 引用，而非逐个修改
    // 这样读取方要么读到旧 map（完整），要么读到新 map（完整），不会出现中间态
    wl.rules = newRules
    fmt.Printf("白名单已重载，共 %d 条规则\n", len(newRules))
}

// Contains 高频查询：使用读锁
func (wl *WhiteList) Contains(ip string) bool {
    wl.mu.RLock()
    defer wl.mu.RUnlock()
    return wl.rules[ip]
}

func main() {
    cb := &CircuitBreaker{state: "closed"}
    
    // 模拟 100 个 goroutine 并发查询状态
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            _ = cb.GetState()  // 100 个并发读，不会阻塞彼此
        }()
    }
    
    // 同时触发熔断（写操作）
    go func() {
        time.Sleep(10 * time.Millisecond)
        cb.Trip()  // 写锁会等待所有读锁释放后才进入
    }()
    
    wg.Wait()
}
```

**关键点**：`RWMutex` 的读锁不互斥，但**写锁会阻塞新的读锁**。如果写操作频繁，读 goroutine 可能"饿死"（长期拿不到锁），此时应降级为 `Mutex`。

---

#### 2. `sync/atomic` — 无锁原子操作

- `sync/atomic`：直接映射到 CPU 原子指令，**无内核态切换**，适合极高频计数器。
- 直接调用 CPU 提供的原子指令（如 x86 的 LOCK XADD），不涉及操作系统调度，不引发 goroutine 阻塞切换，因此比 Mutex 快 1~2 个数量级。
局限：只能操作整型（int32/64、uint32/64、uintptr）和指针，无法保护复杂数据结构。
本项目注意：
- InFlight（当前处理中请求数）是高频增减的热点变量，用 atomic 避免成为性能瓶颈。
- RoundRobin 索引只需原子递增后取模，无需锁保护。
- SWRR（Smooth Weighted Round Robin）的当前权重 currentWeight 也是原子变量，确保多 goroutine 调度时权重计算正确。
```go
package main

import (
    "fmt"
    "sync"
    "sync/atomic"
)

// BackendNode 后端节点，用于负载均衡
type BackendNode struct {
    // 使用 atomic.Int64 替代普通 int64，确保并发安全
    InFlight      atomic.Int64  // 当前处理中的请求数（热点变量）
    RoundRobinIdx atomic.Int64  // RoundRobin 轮询索引
    CurWeight     atomic.Int64  // SWRR 当前权重
    Weight        int64          // 静态配置权重（只读，无需原子）
    Addr          string
}

// IncrementInFlight 请求进入时原子递增
func (n *BackendNode) IncrementInFlight() {
    // Add(1) 直接映射到 CPU LOCK XADD 指令，纳秒级完成
    n.InFlight.Add(1)
}

// GetInFlight 获取当前值
func (n *BackendNode) GetInFlight() int64 {
    return n.InFlight.Load()
}

// NextRoundRobin 原子递增并取模，返回选中的节点索引
func NextRoundRobin(nodes []*BackendNode) *BackendNode {
    if len(nodes) == 0 {
        return nil
    }
    // 使用全局原子计数器，所有 goroutine 共享
    var globalIdx atomic.Int64
    
    idx := globalIdx.Add(1) % int64(len(nodes))
    return nodes[idx]
}

// SWRRNext Smooth Weighted Round Robin 选择节点
func SWRRNext(nodes []*BackendNode) *BackendNode {
    var best *BackendNode
    var total int64 = 0
    
    for _, n := range nodes {
        // 原子读取当前权重
        cw := n.CurWeight.Load()
        cw += n.Weight       // cw = cw + weight
        n.CurWeight.Store(cw) // 原子写回
        total += n.Weight
        
        if best == nil || cw > best.CurWeight.Load() {
            best = n
        }
    }
    
    if best != nil {
        // 原子递减：best.CurWeight -= total
        best.CurWeight.Add(-total)
    }
    return best
}

func main() {
    node := &BackendNode{Addr: "10.0.0.1:8080", Weight: 5}
    
    var wg sync.WaitGroup
    for i := 0; i < 10000; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            node.IncrementInFlight()
        }()
    }
    wg.Wait()
    
    fmt.Printf("InFlight: %d (应为 10000)\n", node.GetInFlight())
}
```

**关键点**：`atomic` 只保证**单个变量的原子性**，不保证**多变量之间的顺序一致性**。如果业务逻辑需要同时修改 `InFlight` 和 `CurWeight` 且两者必须强一致，仍需用 `Mutex`。

---

#### 3. `sync.Pool` — 对象复用

复用临时对象，减轻 GC 压力。**GC 时会清空池内对象**。

- 本质是一个线程安全的对象缓存池。调用 Get() 取对象，Put() 归还。若池为空，Get() 返回 nil（需自行初始化）。
- 核心特性：每次 GC 运行时，Pool 内所有对象会被无条件清空。这是 Go 的设计，目的是防止 Pool 变成长期引用、阻碍垃圾回收。
- 本项目注意：32KB 的 []byte 缓冲区如果每次请求都 make，在高并发下会给 GC 带来巨大压力。使用 sync.Pool 复用后，相同内存被反复利用，显著降低分配次数和 GC 频率。但绝不能把 Pool 当作持久缓存——如果业务逻辑要求对象必须长期存活，Pool 不适合。
```go
package main

import (
    "fmt"
    "sync"
)

// BufferPool 复用 32KB 的缓冲区，用于读写 HTTP Body
var BufferPool = sync.Pool{
    // New 函数：当 Pool 为空时，调用此函数创建新对象
    New: func() interface{} {
        // 分配 32KB 的 []byte
        buf := make([]byte, 32*1024)
        fmt.Println("Pool 为空，新建 32KB buffer")
        return &buf  // 返回指针，避免接口转换时的拷贝
    },
}

// ProcessRequest 模拟网关处理请求
func ProcessRequest(bodySize int) {
    // 从池中取出一个 buffer（可能是复用的，也可能是新创建的）
    bufPtr := BufferPool.Get().(*[]byte)
    buf := *bufPtr
    
    // 使用 buffer 处理数据
    if len(buf) < bodySize {
        // 如果不够大，扩容（实际项目中应避免频繁扩容）
        newBuf := make([]byte, bodySize)
        bufPtr = &newBuf
        buf = newBuf
    }
    
    // 模拟写入数据
    for i := 0; i < bodySize && i < len(buf); i++ {
        buf[i] = byte(i % 256)
    }
    
    fmt.Printf("处理 %d 字节数据，buffer 容量 %d\n", bodySize, len(buf))
    
    // 使用完毕后归还到池中，供下一个请求复用
    // 注意：归还前无需清空（reset），因为下一个使用者会覆盖写入
    BufferPool.Put(bufPtr)
}

func main() {
    // 第一次：Pool 为空，触发 New 创建
    ProcessRequest(1000)
    
    // 第二次：复用第一次归还的 buffer，不再触发 New
    ProcessRequest(2000)
    
    // 第三次：继续复用
    ProcessRequest(500)
    
    // 触发 GC 后，Pool 内对象会被清空
    // 下一次 Get 将再次触发 New
    fmt.Println("--- 触发 GC ---")
    // runtime.GC() // 若手动调用，Pool 会被清空
}
```

**关键点**：
- **必须 `Put` 归还**：如果取出后永不归还，Pool 退化为普通分配器，失去复用意义。
- **不能放有状态对象**：因为 GC 会清空 Pool，且对象可能被其他 goroutine 拿到，**绝不能把 Pool 当作数据库连接池或会话缓存**。
- **存指针而非值**：`sync.Pool` 的接口是 `interface{}`，存大对象时应存指针（`*[]byte`），避免值拷贝开销。

---

#### 4. `sync.Once` — 惰性初始化

确保某段代码在并发环境下**只执行一次**，后续调用零开销。
- 内部通过一个原子变量标记"是否已执行"，第一次调用时走慢路径（加锁执行目标函数），后续调用直接原子读标记并返回，后续调用几乎零开销。
- 本项目注意：反向代理对象（如 httputil.ReverseProxy）的创建可能涉及解析配置、建立连接池等较重操作，使用 sync.Once 确保只创建一次，且多个并发请求不会触发重复创建。追踪器（Tracer）的初始化同理。

```go
package main

import (
    "fmt"
    "net/http/httputil"
    "net/url"
    "sync"
)

// GatewayBackend 网关后端
type GatewayBackend struct {
    targetURL *url.URL
    
    // once 确保 ReverseProxy 只创建一次
    once      sync.Once
    
    // proxy 是惰性创建的对象，可能被多个 goroutine 并发访问
    proxy     *httputil.ReverseProxy
}

// Proxy 获取反向代理实例（线程安全且惰性）
func (gb *GatewayBackend) Proxy() *httputil.ReverseProxy {
    // 无论多少 goroutine 同时调用，once.Do 内的函数只执行一次
    gb.once.Do(func() {
        fmt.Println("【首次】创建 ReverseProxy，解析目标 URL...")
        
        // 创建反向代理：涉及 URL 解析、Transport 配置、连接池初始化
        // 这是一个较重的操作，不应重复执行
        gb.proxy = httputil.NewSingleHostReverseProxy(gb.targetURL)
        
        // 可以在这里配置自定义 Transport、错误处理、超时等
        gb.proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
            w.WriteHeader(http.StatusBadGateway)
            w.Write([]byte("后端服务不可用"))
        }
    })
    
    // 首次之后的所有调用，直接返回已创建的 proxy，无锁、无阻塞
    return gb.proxy
}

// Tracer 分布式追踪器
type Tracer struct {
    once sync.Once
    initErr error
}

// Init 初始化追踪器（如连接 Jaeger/Zipkin）
func (t *Tracer) Init() error {
    t.once.Do(func() {
        fmt.Println("【首次】初始化追踪器...")
        // 模拟初始化：建立连接、注册服务名等
        // t.initErr = initJaeger()
    })
    return t.initErr
}

func main() {
    backend := &GatewayBackend{
        targetURL: &url.URL{Scheme: "http", Host: "localhost:8080"},
    }
    
    var wg sync.WaitGroup
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func(id int) {
            defer wg.Done()
            p := backend.Proxy()  // 100 个 goroutine 同时调用
            fmt.Printf("Goroutine %d 拿到 proxy 指针: %p\n", id, p)
        }(i)
    }
    wg.Wait()
    
    // 输出中只有一条 "【首次】创建 ReverseProxy"，证明只执行了一次
}
```

**关键点**：
- `sync.Once` 的 `Do` 方法**没有返回值**，如果初始化可能报错，需通过闭包捕获错误（如上面的 `initErr`）。
- **不能重置**：`sync.Once` 没有 `Reset` 方法。如果业务需要"重新初始化"，不能用 `sync.Once`，需改用 `atomic.Value` 或 `Mutex`。

---

#### 5. `chan struct{}` — 零内存信号通道

`struct{}` 不占内存，适合纯信号通知（而非数据传输）。
- struct{} 在 Go 中不占任何内存（unsafe.Sizeof(struct{}{}) == 0），因此 chan struct{} 是传递"纯信号"的最经济方式：关闭通道即广播信号，发送空结构体即单播信号。

本项目注意：
- 限流器 GC 停机：当限流器需要退出时，关闭一个 chan struct{}，监听该通道的 goroutine 收到零值后优雅退出，避免 goroutine 泄漏。
- 并发信号量：通过缓冲通道的容量控制并发数，如 sem := make(chan struct{}, 100)，每个请求进入前 sem <- struct{}{}，完成后 <-sem，天然实现"最多 100 并发"的限流。

```go
package main

import (
    "fmt"
    "time"
)

// RateLimiter 带优雅停机的限流器
type RateLimiter struct {
    stopCh   chan struct{}   // 停机信号：关闭此通道即广播"停止工作"
    tokens   chan struct{}   // 令牌桶：容量即最大并发数
}

// NewRateLimiter 创建限流器：maxConcurrent 为最大并发数
func NewRateLimiter(maxConcurrent int) *RateLimiter {
    return &RateLimiter{
        stopCh: make(chan struct{}),
        tokens: make(chan struct{}, maxConcurrent), // 缓冲通道 = 信号量
    }
}

// Acquire 获取一个执行许可（阻塞直到获取成功或收到停机信号）
func (rl *RateLimiter) Acquire() bool {
    select {
    case rl.tokens <- struct{}{}:  // 向通道发送空结构体，占用一个槽位
        return true                 // 成功获取令牌
    case <-rl.stopCh:              // 若 stopCh 被关闭，此处立即得到零值
        return false                // 收到停机信号，拒绝新请求
    }
}

// Release 归还执行许可
func (rl *RateLimiter) Release() {
    select {
    case <-rl.tokens:  // 从通道取出一个空结构体，释放一个槽位
    default:            // 防止重复释放导致死锁
    }
}

// Stop 优雅停机：关闭信号通道，广播停机指令
func (rl *RateLimiter) Stop() {
    close(rl.stopCh)  // 关闭后，所有监听 stopCh 的 goroutine 都会收到通知
    fmt.Println("限流器停机信号已广播")
}

// Worker 模拟一个工作 goroutine
func (rl *RateLimiter) Worker(id int, task string) {
    if !rl.Acquire() {
        fmt.Printf("Worker %d: 收到停机信号，拒绝任务 %s\n", id, task)
        return
    }
    defer rl.Release()
    
    fmt.Printf("Worker %d: 开始处理 %s\n", id, task)
    time.Sleep(100 * time.Millisecond)
    fmt.Printf("Worker %d: 完成 %s\n", id, task)
}

func main() {
    // 创建并发数为 3 的限流器
    rl := NewRateLimiter(3)
    
    // 启动 10 个任务
    for i := 0; i < 10; i++ {
        go rl.Worker(i, fmt.Sprintf("task-%d", i))
    }
    
    time.Sleep(50 * time.Millisecond)
    
    // 模拟系统收到 SIGTERM，触发停机
    rl.Stop()
    
    // 再尝试提交新任务
    go rl.Worker(99, "late-task")
    
    time.Sleep(200 * time.Millisecond)
}
```

**关键点**：
- **关闭通道广播**：`close(stopCh)` 后，所有 `<-stopCh` 的接收方都能立刻得到零值，实现**一对多广播**。这是通道最优雅的用法之一。
- **缓冲通道作信号量**：`make(chan struct{}, N)` 的容量就是信号量上限。发送占用，接收释放，天然实现"最多 N 并发"。
- **select 非阻塞检查**：`Release` 中的 `default` 防止在通道已满时阻塞（虽然正常逻辑不应出现这种情况，但作为防御性编程）。

---

#### 6. CAS 循环 (`CompareAndSwap`) — 乐观锁

**Compare-And-Swap**：只有当内存值等于预期值 `old` 时，才将其更新为 `new`，否则失败重试。
- CAS（Compare-And-Swap） 是 CPU 提供的原子指令：只有当某个内存地址的当前值等于预期值 old 时，才将其更新为新值 new，否则不做任何操作并返回失败。
- "循环"的含义：因为 CAS 可能失败（其他 goroutine 抢先修改了值），所以需要用 for 循环不断重试：重新读取当前值 → 计算新值 → 再次 CAS，直到成功。
- 与 atomic.Add 的区别：Add 只能做简单的加减，无法做"条件判断后再修改"。DecrementInFlight 需要先判断 old > 0，才能决定是否减 1，这是 Add(-1) 做不到的。

```go
package main

import (
    "fmt"
    "sync"
    "sync/atomic"
)

// BackendNode 后端节点
type BackendNode struct {
    InFlight atomic.Int64  // 当前正在处理的请求数
    Addr     string
}

// IncrementInFlight 请求进入：简单原子递增
func (n *BackendNode) IncrementInFlight() {
    n.InFlight.Add(1)
}

// DecrementInFlight 请求完成：CAS 循环确保不会减到负数
func (n *BackendNode) DecrementInFlight() {
    for {
        old := n.InFlight.Load()  // Step 1: 读取当前值
        if old <= 0 {
            // Step 2: 业务条件判断——已归零则不再递减
            // 这防止了以下场景：
            // - 请求超时后，业务层和网关层都调用了 Decrement，导致重复减
            // - 某个请求异常退出，未先调用 Increment，直接调了 Decrement
            return
        }
        
        // Step 3: 尝试原子交换
        // 语义：如果 n.InFlight 的当前值 == old，则设为 old-1，返回 true
        //       否则不做任何事，返回 false
        if n.InFlight.CompareAndSwap(old, old-1) {
            return  // 成功，退出循环
        }
        
        // Step 4: CAS 失败 → 其他 goroutine 抢先修改了值
        // 循环回到 Step 1，重新读取最新值
        // 这种自旋在竞争不激烈时极快（几个纳秒），比 Mutex 的阻塞-唤醒快得多
    }
}

// SafeDecrement 带重试次数上限的 CAS（防止极端竞争下无限自旋）
func (n *BackendNode) SafeDecrement(maxRetries int) bool {
    for i := 0; i < maxRetries; i++ {
        old := n.InFlight.Load()
        if old <= 0 {
            return false
        }
        if n.InFlight.CompareAndSwap(old, old-1) {
            return true
        }
        // 可选：每次失败让出时间片，降低 CPU 占用
        // runtime.Gosched()
    }
    // 重试耗尽，降级为 Mutex 或报错
    return false
}

// 模拟并发场景：100 个请求进入，但只完成 80 个（20 个超时未回调）
func main() {
    node := &BackendNode{Addr: "10.0.0.1:8080"}
    
    var wg sync.WaitGroup
    
    // 100 个请求进入
    for i := 0; i < 100; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            node.IncrementInFlight()
        }()
    }
    wg.Wait()
    fmt.Printf("100 个请求进入后，InFlight = %d\n", node.InFlight.Load())
    
    // 只有 80 个正常完成
    for i := 0; i < 80; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            node.DecrementInFlight()
        }()
    }
    wg.Wait()
    fmt.Printf("80 个请求完成后，InFlight = %d\n", node.InFlight.Load())
    
    // 模拟重复 Decrement（如超时回调 + 正常回调重复触发）
    for i := 0; i < 30; i++ {
        wg.Add(1)
        go func() {
            defer wg.Done()
            node.DecrementInFlight()  // 多余的 Decrement 会被 CAS 拦截
        }()
    }
    wg.Wait()
    fmt.Printf("30 次额外 Decrement 后，InFlight = %d（不会为负）\n", node.InFlight.Load())
}
```

**关键点**：
- **为什么不用 `atomic.Add(-1)`？** `Add` 无法做"条件判断"。如果当前值已经是 0，`Add(-1)` 会直接变成 -1，导致统计失真。
- **ABA 问题在此无害**：即使 `old` 经历了 1→2→1 的变化，最终结果也只是多一次循环重试，不会导致逻辑错误。
- **自旋 vs 阻塞**：CAS 循环是"自旋"（忙等待），适合**极短临界区**（如一个整数的修改）。如果循环体内逻辑复杂或竞争激烈，应改用 `Mutex` 或加入退避策略（`runtime.Gosched()`）。
**CAS 循环示例**（`DecrementInFlight`）：
```go
// 为何不用简单的 Add(-1)？因为 Add(-1) 可能让 InFlight 变为负数。
// CAS 循环：读取当前值 → 若已 ≤ 0 则放弃 → 否则尝试原子交换为 old-1。
// 若交换失败（被其他 goroutine 抢先），则重试整个循环。
func (n *BackendNode) DecrementInFlight() {
    for {
        old := n.InFlight.Load()
        if old <= 0 {
            return  // 已为零，不再递减
        }
        if n.InFlight.CompareAndSwap(old, old-1) {
            return  // 成功减 1
        }
        // CAS 失败 → 重试
    }
}
```

### P1：HTTP 反向代理原理

Go 标准库 `net/http/httputil.ReverseProxy` 是 HTTP 反向代理的基础：

```
客户端 ──HTTP Request──▶ ReverseProxy ──HTTP Request──▶ 后端服务器
客户端 ◀──HTTP Response── ReverseProxy ◀──HTTP Response── 后端服务器
```

关键接口：
- `Transport`：控制如何建立到后端的连接（连接池、TLS、超时）
- `BufferPool`：提供拷贝缓冲区（避免每次请求分配新内存）
- `ErrorHandler`：后端不可达时的错误处理回调

本项目中，所有代理共享同一个 `sharedTransport`（全局连接池），并通过 `byteBufferPool`（`sync.Pool`）复用 32KB 缓冲区。
**HTTP 反向代理**的本质是**"流量中转站"**：它站在客户端和后端服务器之间，对外暴露统一入口，对内将请求分发到各个后端实例，并把响应原路返回。`net/http/httputil.ReverseProxy` 是 Go 标准库提供的工业级实现，下面从原理到源码级细节逐层拆解。

---

#### 一、ReverseProxy 的核心工作流程

```text
┌─────────┐   HTTP Request    ┌──────────────────┐   HTTP Request    ┌──────────────┐
│ 客户端  │ ────────────────▶ │ ReverseProxy     │ ────────────────▶ │ 后端服务器    │
│ (浏览器)│                    │ (service-hub)    │                   │ (citizen-svc) │
│         │ ◀──────────────── │                  │ ◀──────────────── │              │
└─────────┘   HTTP Response   └──────────────────┘   HTTP Response   └──────────────┘
                              修改请求头(X-Forwarded-For)
                              复用连接池、复用缓冲区
                              错误时返回502/504
```

**一次完整转发的时序：**

1. **接收**：`ReverseProxy` 作为 `http.Handler`，先接收客户端的 `*http.Request`
2. **克隆**：深度克隆请求对象（避免修改原始请求），重写 `RequestURI`、`Host`、`X-Forwarded-*` 等头部
3. **拨号**：通过 `Transport.RoundTrip(clonedReq)` 向后端发起 HTTP 请求
4. **流式拷贝**：收到后端响应后，将 `StatusCode`、`Header` 复制给客户端，再通过 `io.CopyBuffer` 将 Body 泵回客户端
5. **清理**：请求结束后归还缓冲区、关闭或复用连接

---

#### 二、三大关键接口详解

##### 1. Transport — 连接与请求的生命线

`Transport` 是 `http.RoundTripper` 接口，决定了**"如何与后端建立通信"**。如果不指定，`ReverseProxy` 默认使用 `http.DefaultTransport`。

```go
type ReverseProxy struct {
    Transport http.RoundTripper  // 默认 http.DefaultTransport
    // ...
}
```

**Transport 控制的核心能力：**

| 能力 | 配置项 | 本项目意义 |
|------|--------|-----------|
| **连接池** | `MaxIdleConns`、`MaxIdleConnsPerHost` | 避免每个请求都新建 TCP 连接，后端压力降低 90% |
| **连接超时** | `DialContext.Timeout` | 防止后端僵死导致 goroutine 堆积 |
| **请求超时** | `ResponseHeaderTimeout` | 后端响应头超过 N 秒未返回即断开 |
| **TLS 配置** | `TLSClientConfig` | 政务云内网 mTLS 双向认证 |
| **HTTP/2** | `ForceAttemptHTTP2` | 支持 gRPC-over-HTTP/2 透传 |

**本项目实践：共享 `sharedTransport`**

```go
// 全局共享 Transport，所有反向代理实例共用同一个连接池
var sharedTransport = &http.Transport{
    // 连接池：最多空闲 1024 个连接，每个后端主机最多 100 个空闲连接
    MaxIdleConns:        1024,
    MaxIdleConnsPerHost: 100,
    MaxConnsPerHost:     200,  // 硬上限，防止单后端被打爆
    
    // 超时控制
    DialContext: (&net.Dialer{
        Timeout:   3 * time.Second,  // TCP 握手 3 秒超时
        KeepAlive: 30 * time.Second, // TCP KeepAlive 探活
    }).DialContext,
    
    ResponseHeaderTimeout: 10 * time.Second, // 等后端响应头最多 10 秒
    IdleConnTimeout:       90 * time.Second, // 空闲连接 90 秒回收
    
    // 政务云 TLS：双向 mTLS
    TLSClientConfig: &tls.Config{
        Certificates: []tls.Certificate{clientCert}, // 客户端证书
        RootCAs:      trustedCAs,                    // 校验后端服务端证书
    },
    
    ForceAttemptHTTP2: true, // 支持 HTTP/2 多路复用
}

// 创建 ReverseProxy 时注入共享 Transport
proxy := httputil.NewSingleHostReverseProxy(targetURL)
proxy.Transport = sharedTransport  // 所有 proxy 共享同一连接池
```

**为什么必须共享？**  
如果每个后端都创建一个独立的 `Transport`，每个 `Transport` 内部都有自己的连接池和 goroutine（用于连接健康检查、超时处理）。100 个后端就会产生 100 个连接池，内存暴涨且无法复用连接。共享后，到同一后端的多个请求可以复用同一个 TCP 连接上的 Keep-Alive。

---

##### 2. BufferPool — 零拷贝缓冲区的复用中枢

`ReverseProxy` 在转发 Body 时需要一块临时内存作为拷贝桥梁。默认行为是**每次请求都 `make([]byte, 32KB)`**，高并发下 GC 压力巨大。

```go
type ReverseProxy struct {
    BufferPool BufferPool  // 缓冲池接口
    // ...
}

// 标准库定义的接口，极其简单
type BufferPool interface {
    Get() []byte
    Put([]byte)
}
```

**本项目实践：`sync.Pool` 实现 `BufferPool`**

```go
// byteBufferPool 复用 32KB 缓冲区
type byteBufferPool struct {
    pool sync.Pool
}

func newByteBufferPool() *byteBufferPool {
    return &byteBufferPool{
        pool: sync.Pool{
            New: func() interface{} {
                // 统一分配 32KB，覆盖绝大多数 HTTP Body 场景
                b := make([]byte, 32*1024)
                return &b  // 返回指针，避免接口转换时拷贝
            },
        },
    }
}

// Get 取出缓冲区
func (p *byteBufferPool) Get() []byte {
    return *(p.pool.Get().(*[]byte))
}

// Put 归还缓冲区
func (p *byteBufferPool) Put(b []byte) {
    // 防御性检查：只回收长度匹配的，防止超大 Body 污染池子
    if cap(b) == 32*1024 {
        p.pool.Put(&b)
    }
    // 不匹配的直接丢弃，由 GC 回收
}

// 全局单例
var sharedBufferPool = newByteBufferPool()

// 创建代理时注入
proxy.BufferPool = sharedBufferPool
```

**内部调用时机：**  
当 `ReverseProxy` 执行 `io.CopyBuffer` 转发响应 Body 时，会调用 `BufferPool.Get()` 获取缓冲区；拷贝完成后调用 `Put()` 归还。因为 `sync.Pool` 的复用机制，同一块 32KB 内存可以在毫秒级被下一个请求复用，**分配次数趋近于零**。

---

##### 3. ErrorHandler — 后端故障时的兜底策略

当 `Transport.RoundTrip` 返回错误（如连接拒绝、超时、TLS 握手失败），或后端返回的响应需要被拦截时，`ReverseProxy` 会调用 `ErrorHandler`，而不是直接 panic 或返回空响应。

```go
type ReverseProxy struct {
    ErrorHandler func(http.ResponseWriter, *http.Request, error)
    // ...
}
```

**默认行为（未设置时）**：返回 `502 Bad Gateway`，Body 为空，对客户端极不友好。

**本项目实践：政务云精细化错误处理**

```go
proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
    // 1. 记录结构化日志，便于后续链路追踪
    logger.Error("proxy_error",
        zap.String("path", r.URL.Path),
        zap.String("backend", r.URL.Host),
        zap.Error(err),
    )
    
    // 2. 根据错误类型映射到不同 HTTP 状态码，便于前端/调用方区分重试策略
    var status int
    var msg string
    
    switch {
    case os.IsTimeout(err), errors.Is(err, context.DeadlineExceeded):
        status = http.StatusGatewayTimeout  // 504
        msg = "后端服务响应超时，请稍后重试"
        
    case errors.Is(err, context.Canceled):
        status = http.StatusServiceUnavailable  // 503
        msg = "请求已被取消"
        
    default:
        // 连接拒绝、DNS 失败、TLS 错误等
        status = http.StatusBadGateway  // 502
        msg = "后端服务暂不可用"
    }
    
    // 3. 清理已写入的部分 Header，重新写入错误响应
    // 注意：此时 w 可能已写入部分数据，需确保不会触发 http: superfluous response.WriteHeader call
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    w.WriteHeader(status)
    json.NewEncoder(w).Encode(map[string]interface{}{
        "error":   msg,
        "status":  status,
        "path":    r.URL.Path,
        "trace_id": r.Header.Get("X-Trace-ID"),  // 政务云审计要求
    })
    
    // 4. 触发熔断器计数（如果配置了的话）
    if cb, ok := r.Context().Value("circuit_breaker").(*CircuitBreaker); ok {
        cb.RecordFailure()
    }
}
```

**关键点：**
- `ErrorHandler` 中收到的 `error` 是 `Transport.RoundTrip` 的原始错误，可能是 `net/url.Error`、`tls.CertificateVerificationError`、`context.DeadlineExceeded` 等，需要类型断言或 `errors.Is` 区分。
- 一旦进入 `ErrorHandler`，说明**后端完全没有返回有效响应**（连 HTTP Header 都没收到），因此必须自己构造完整的 HTTP 响应给客户端。

---

#### 三、完整配置示例：政务云网关中的 ReverseProxy

```go
package main

import (
    "net/http"
    "net/http/httputil"
    "net/url"
    "time"
)

// GatewayBackend 封装一个后端及其代理
type GatewayBackend struct {
    target *url.URL
    proxy  *httputil.ReverseProxy
}

func NewGatewayBackend(rawURL string) (*GatewayBackend, error) {
    target, err := url.Parse(rawURL)
    if err != nil {
        return nil, err
    }
    
    proxy := httputil.NewSingleHostReverseProxy(target)
    
    // 1. 共享连接池：所有后端共用，避免连接爆炸
    proxy.Transport = sharedTransport
    
    // 2. 共享缓冲池：32KB 复用，降低 GC
    proxy.BufferPool = sharedBufferPool
    
    // 3. 自定义错误处理：政务云要求结构化错误 + 审计日志
    proxy.ErrorHandler = sharedErrorHandler
    
    // 4. 修改请求：注入政务云必需的审计头
    originalDirector := proxy.Director
    proxy.Director = func(req *http.Request) {
        originalDirector(req)  // 先执行默认行为（重写 Host、Scheme 等）
        
        // 追加政务云审计头
        req.Header.Set("X-Forwarded-For", getClientIP(req))
        req.Header.Set("X-Gateway-Time", time.Now().UTC().Format(time.RFC3339))
        req.Header.Set("X-Request-ID", generateRequestID())
        
        // 剥离客户端传来的敏感头（防止伪造内网身份）
        req.Header.Del("X-Internal-Token")
    }
    
    return &GatewayBackend{
        target: target,
        proxy:  proxy,
    }, nil
}

// ServeHTTP 实现 http.Handler 接口
func (gb *GatewayBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
    gb.proxy.ServeHTTP(w, r)
}
```

---

#### 四、性能与资源控制全景

| 组件 | 不优化的后果 | 本项目的优化手段 |
|------|-------------|----------------|
| **Transport** | 每次请求新建 TCP 连接，后端 SYN Flood，延迟 3 次握手 | 全局共享连接池 + Keep-Alive + 超时控制 |
| **BufferPool** | 每次请求 `make([]byte, 32KB)`，1万 QPS 时 GC 每秒扫描几百 MB 临时内存 | `sync.Pool` 复用，分配趋近于零 |
| **ErrorHandler** | 后端故障时返回空白 502，客户端无法区分"超时"和"宕机"，疯狂重试压垮网关 | 精细化错误分类 + 结构化 JSON + 触发熔断 |

---

#### 五、一句话总结

> `httputil.ReverseProxy` 是 Go 的**"HTTP 流量泵"**：  
> `Transport` 决定**"怎么连后端"**（连接池是命脉），  
> `BufferPool` 决定**"怎么搬数据"**（复用缓冲区是性能关键），  
> `ErrorHandler` 决定**"后端挂了怎么办"**（政务云要求优雅降级而非裸奔 502）。  
> 三者全部共享全局实例，是支撑 `service-hub` 万级并发、毫秒延迟的底层基石。

### P2：gRPC 流式通信模型

gRPC 支持四种通信模式：

| 模式 | 说明 | 本项目使用 |
|---|---|---|
| Unary（一元） | 客户端发一个请求，服务端返回一个响应 | 隐私原语调用 |
| Server Streaming | 客户端发一个请求，服务端返回流 | — |
| Client Streaming | 客户端发送流，服务端返回一个响应 | — |
| **Bidirectional Streaming** | 双向流，双方可同时发送和接收 | **gRPC 透明代理** |

本项目的 gRPC 代理使用 `UnknownServiceHandler` 拦截所有未注册的 gRPC 方法，通过 `rawCodec` 实现零编解码字节透传，配合两个 goroutine 实现双向流转发。

### P3：mTLS 双向认证基础

标准 TLS 只验证服务端身份（客户端验证服务端证书）。mTLS（Mutual TLS）在此基础上增加客户端证书验证：

```
标准 TLS：  客户端 ──验证──▶ 服务端证书
mTLS：      客户端 ──验证──▶ 服务端证书
            服务端 ──验证──▶ 客户端证书  ← 新增
```

在本项目中：
- **内部 CA**：项目自建的证书颁发机构（`config/certs/ca.crt`）
- **服务端证书**：Agent 持有（`server.crt`），客户端验证 Agent 身份
- **客户端证书**：Gateway 持有（`client.crt`），Agent 验证 Gateway 身份
- **CN（Common Name）**：证书中的主体标识，如 `service-hub.privshield.internal`
- **白名单**：通过 YAML 配置哪些 CN 允许调用哪些 gRPC 方法

### P4：Prometheus 指标类型

| 类型 | 说明 | 本项目使用 |
|---|---|---|
| Counter | 只增不减的计数器 | `privshield_requests_total` |
| Gauge | 可增可减的瞬时值 | `privshield_gateway_backend_in_flight` |
| Histogram | 按桶分布的观测值 | `privshield_request_duration_seconds` |
| Summary | 分位数观测（客户端计算） | — |

**RED 方法论**（Tom Wilkie 提出）：
- **Rate**（请求率）：每秒请求数 → Counter
- **Errors**（错误率）：5xx 响应占比 → Counter 派生
- **Duration**（延迟）：请求耗时分布 → Histogram

### P5：令牌桶限流算法

令牌桶（Token Bucket）是一种平滑限流算法：

```
           令牌以恒定速率 rps 补充
                    │
                    ▼
         ┌──────────────────┐
         │    令牌桶 (burst)  │  ← 桶容量 = 突发上限
         │  ● ● ● ● ● ○ ○   │  ← ● = 可用令牌, ○ = 空位
         └──────────────────┘
                    │
                    ▼
              每个请求消耗 1 令牌
              桶空时拒绝请求 (429)
```

关键参数：
- `rps`（Rate Per Second）：令牌补充速率
- `burst`：桶容量（允许的最大突发量）
- 本项目使用 **32 分片** 降低锁竞争
  以下是 **P5：令牌桶限流算法** 的完整补充说明，重点展开 **32 分片降低锁竞争** 的工程实现原理。

---

#### 一、算法原理：时间驱动的虚拟令牌

令牌桶不是真的有一个后台线程每隔 1/rps 秒往桶里扔一个令牌。工程上的高效实现是**"惰性计算（Lazy Evaluation）"**：

```
请求到达时：
  1. 计算距离上次请求过去了多久（Δt）
  2. 按 rps 速率算出这段时间应补充多少令牌：Δt × rps
  3. 加到当前剩余令牌中，但不超过桶容量 burst
  4. 如果有 ≥1 个令牌，消耗 1 个并放行；否则拒绝（429）
```

**关键洞察**：令牌补充是**"算出来"**的，不是**"发出来"**的。这样无需定时器，无需后台 goroutine，纯请求触发。

---

#### 二、关键参数的数学含义

| 参数 | 数学定义 | 实际影响 |
|------|---------|---------|
| **rps** | 令牌补充速率（tokens/second） | 长期平均 QPS 上限。即使瞬间空闲，后续也只能按此速率恢复令牌 |
| **burst** | 桶容量（最大令牌数） | 允许的瞬间突发流量。例如 burst=100，意味着可以瞬间通过 100 个请求，之后被限制到 rps |

**举例**：
- `rps=10, burst=50`
- 冷启动时桶里有 50 个令牌，前 50 个请求瞬间通过（突发）
- 第 51 个请求到达时，桶空了，必须等令牌以 10/s 的速率慢慢恢复
- 长期看，平均 QPS 被压制在 10

---

#### 三、单桶的锁瓶颈：为什么必须分片？

如果整个网关只有一个全局令牌桶：

```go
type TokenBucket struct {
    mu       sync.Mutex
    tokens   float64
    lastTime time.Time
}

func (tb *TokenBucket) Allow() bool {
    tb.mu.Lock()         // 🔒 所有请求串行抢这一把锁
    defer tb.mu.Unlock()
    // ... 计算、扣减
}
```

**问题**：假设网关 10,000 QPS，每个请求都要先抢这把全局锁。这意味着：
- 10,000 个 goroutine 在 `tb.mu.Lock()` 处排队
- 锁竞争导致上下文切换，CPU 大量消耗在 `futex` 和调度上
- 延迟从微秒级飙升到毫秒级，**限流器本身成为性能瓶颈**

> 实测数据：单 `sync.Mutex` 在 32 核机器上极限约 3~5 万竞争操作/秒，而 32 分片后可轻松突破 100 万。

---

#### 四、32 分片架构详解

##### 4.1 核心思想：大锁拆小锁

把**一个全局桶**拆成 **32 个独立子桶**，每个子桶有自己的锁、自己的令牌计数、自己的时间戳。

```
请求到达
    │
    ▼
┌─────────────┐
│  分片选择器   │  ← 决定用 32 个桶中的哪一个
└──────┬──────┘
       │
   ┌───┴───┬───────┬───────┐
   ▼       ▼       ▼       ▼
 [桶0]   [桶1]   ...   [桶31]
  │││     │││           │││
  每个桶独立持有一把 sync.Mutex
  每个桶独立维护 tokens 和 lastTime
```

##### 4.2 分片选择策略

| 策略 | 实现 | 优点 | 缺点 |
|------|------|------|------|
| **随机** | `rand.Int31n(32)` | 负载极均匀 | `rand` 全局锁有开销 |
| **轮询** | `atomic.AddUint32(&counter, 1) % 32` | **无锁、最均匀** | 需维护计数器 |
| **基于请求特征** | `hash(clientIP) % 32` | 同一客户端固定到同一桶，避免局部突发挤占全局 | 可能哈希不均导致热点 |

**本项目推荐轮询**（兼顾性能与公平）：

```go
var shardCounter atomic.Uint32

func (s *ShardedTokenBucket) pickShard() uint32 {
    return shardCounter.Add(1) % 32  // 原子递增，无锁，32 轮一圈
}
```

##### 4.3 参数拆分：总量如何分配到 32 片？

不是每个桶都有完整的 `rps` 和 `burst`，否则总量会变成 32 倍。正确做法是**均分**：

```go
perShardRPS   = totalRPS   / 32.0
perShardBurst = totalBurst / 32.0
```

**为什么这样是"足够好"的近似？**
- 数学上，32 个独立 Poisson 过程的叠加，其长期平均速率等于总和
- 短期看，某个桶可能提前耗尽，但轮询会让后续请求平摊到其他桶，**全局突发能力略大于 burst（最多 31 个余量），但在工程上完全可接受**
- 换来的收益是：**锁竞争概率从 100% 降到约 3%**（假设均匀分布）

---

#### 五、完整 Go 实现

```go
package main

import (
    "math"
    "sync"
    "sync/atomic"
    "time"
)

// ShardedTokenBucket 32分片令牌桶
type ShardedTokenBucket struct {
    shards [32]*bucketShard
    counter atomic.Uint32 // 轮询计数器
}

// bucketShard 单分片
type bucketShard struct {
    mu       sync.Mutex
    tokens   float64   // 当前可用令牌（浮点支持小数累积）
    lastTime time.Time // 上次请求时间
    rps      float64   // 该分片的补充速率
    burst    float64   // 该分片的容量上限
}

// NewShardedTokenBucket 创建分片桶
// totalRPS: 全局每秒令牌数, totalBurst: 全局突发上限
func NewShardedTokenBucket(totalRPS float64, totalBurst int) *ShardedTokenBucket {
    s := &ShardedTokenBucket{}
    perRPS := totalRPS / 32.0
    perBurst := float64(totalBurst) / 32.0
    
    for i := 0; i < 32; i++ {
        s.shards[i] = &bucketShard{
            rps:      perRPS,
            burst:    perBurst,
            tokens:   perBurst,        // 冷启动时满令牌（允许初始突发）
            lastTime: time.Now(),
        }
    }
    return s
}

// Allow 尝试获取一个令牌
func (s *ShardedTokenBucket) Allow() bool {
    // 轮询选择分片：原子递增，无锁，且绝对均匀
    idx := s.counter.Add(1) % 32
    return s.shards[idx].allow()
}

// allow 单分片内部逻辑（受该分片自己的锁保护）
func (sh *bucketShard) allow() bool {
    sh.mu.Lock()
    defer sh.mu.Unlock()
    
    now := time.Now()
    elapsed := now.Sub(sh.lastTime).Seconds()
    sh.lastTime = now
    
    // 1. 按时间补充令牌（惰性计算）
    sh.tokens += elapsed * sh.rps
    if sh.tokens > sh.burst {
        sh.tokens = sh.burst // 不能超过桶容量
    }
    
    // 2. 尝试消费
    if sh.tokens >= 1.0 {
        sh.tokens -= 1.0
        return true // ✅ 放行
    }
    
    return false // ❌ 拒绝，由外层返回 429 Too Many Requests
}
```

**关键点**：
- `sh.mu` 是**分片级锁**，不是全局锁。32 个 goroutine 同时请求时，大概率各拿各的锁，互不阻塞。
- `tokens` 用 `float64`：因为 `rps` 可能是小数（如 `rps=50` 分配到 32 片，每片 `1.5625`），浮点可以精确累积。
- 没有后台 goroutine，纯请求触发计算，**零空闲开销**。

---

#### 六、与漏桶（Leaky Bucket）的区别

| 维度 | 令牌桶（Token Bucket） | 漏桶（Leaky Bucket） |
|------|----------------------|---------------------|
| **流量形状** | 允许**突发**，长期平滑 | **绝对平滑**，突发被整流 |
| **空闲后重启** | 桶会攒满令牌，下次来请求可瞬间通过 burst 个 | 漏桶空着，下次也只能按固定速率通过 1 个 |
| **实现复杂度** | 需记录 lastTime 和 tokens | 通常一个队列 + 固定速率消费者 |
| **适用场景** | 允许业务有合理突发（如用户连点刷新） | 严格限速，保护下游（如支付接口） |

> 本项目选令牌桶，因为政务云网关需要**允许合理的突发流量**（如早晨上班高峰期），而不是生硬地削平所有峰值。

---

#### 七、生产环境注意事项

##### 1. 冷启动浪涌（Thundering Herd）
冷启动时 32 个桶都是满的（`tokens = burst/32`），如果瞬间涌入 `burst` 个请求，会全部放行。这在服务刚发布时可能打死后端。

**解法**：启动时预消耗一部分令牌，或设置 `warmup` 阶段。

##### 2. 分片不均的极端情况
如果用了 `hash(IP) % 32`，而某个大客户的 IP 恰好哈希到同一桶，该桶会提前耗尽，导致该客户被限流，其他客户还有余量。

**解法**：对外部客户用随机/轮询分片；对内部服务间调用，如需保证同一连接的一致性，再用特征哈希。

##### 8. 与 P0 并发原语的呼应
32 分片是 **"用空间换时间、用分治降低锁粒度"** 的经典案例。如果极限性能仍不足，可以进一步：
- 把 `sync.Mutex` 替换为 **CAS 循环**（每个分片用 `atomic.Int64` 存储 tokens 和时间戳）
- 但代码复杂度会显著上升，**32 个 Mutex 在绝大多数生产场景已足够**（竞争概率 < 5% 时，Mutex 性能接近无锁）

---

#### 八、一句话总结

> **令牌桶 = "以 rps 的速率往桶里算账，以 burst 的容量允许瞬间透支"。**  
> **32 分片 = "把一个大账本拆成 32 个小账本，各记各的账，各锁各的锁"。**  
> 单桶的 `sync.Mutex` 在万级 QPS 下是性能瓶颈，32 分片通过轮询将竞争概率降到 1/32，是支撑 `service-hub` 高并发限流的工程基石。
---

## 第 2 章：Day 1-2 网关与流量治理 Review

### 1.1 审查文件清单

| 文件路径 | 行数 | 核心职责 |
|---|:---:|---|
| `pkg/gateway/balancer.go` | 359 | P2C-EWMA 负载均衡器 + 5 种调度策略 + BufferPool + BackendNode |
| `pkg/circuitbreaker/circuitbreaker.go` | 141 | 三态熔断器原语（Closed → Open → HalfOpen） |
| `engine-go/internal/gateway/balancer.go` | 42 | 类型别名桥接层（将 pkg 层类型暴露给 engine-go 内部） |
| `engine-go/internal/gateway/http_proxy.go` | 97 | HTTP 反向代理处理器（Gin HandlerFunc） |
| `engine-go/internal/gateway/grpc_proxy.go` | 310 | gRPC 透明流式代理（RawCodec + 双向零拷贝转发） |
| `engine-go/internal/gateway/backend_tls.go` | 65 | 东西向 mTLS 回源 TLS 配置构建 |
| 对应 `_test.go` 文件 | ~400 | 全部网关组件的单元测试 |

### 1.2 核心知识点详解

#### 1.2.1 P2C-EWMA 负载均衡算法

**源码位置**：`pkg/gateway/balancer.go` `selectP2C()` (L186-222)

**算法原理**：
1. **Power of Two Choices (P2C)**：随机选取两个节点，选择负载较轻的那个。相比简单轮询（Round-Robin），P2C 在异构负载场景下能将最大负载降低到 $O(\log \log n)$，且无需全局状态同步。
2. **EWMA (Exponentially Weighted Moving Average)**：指数移动加权平均延迟，公式为 $ \text{EWMA}_{new} = \alpha \times \text{latency} + (1 - \alpha) \times \text{EWMA}_{old} $，其中 $ \alpha $ 为平滑系数（HTTP 代理用 0.3，gRPC 代理用 0.2）。
3. **负载评分**：$\text{score} = (\text{InFlight} + 1) \times \max(\text{EWMA}, 0.001)$。InFlight+1 防止零除，EWMA 下限 0.001 防止首次请求即获得极端优势。

**为何选择 P2C 而非简单轮询**：
- 轮询假设所有后端性能一致，实际中各节点 CPU/内存/网络延迟差异显著
- P2C 通过 EWMA 延迟感知自动避开慢节点，实现自适应调度
- 相比最少连接（Least-Conn），P2C 的随机双选避免了全局锁竞争

**源码完整实现**：
```go
// pkg/gateway/balancer.go L186-222
// selectP2C 幂律双选 (Power of Two Choices) + EWMA 延迟
func (lb *LoadBalancer) selectP2C() *BackendNode {
    if len(lb.nodes) == 0 {
        return nil
    }

    // 收集可用节点（熔断器允许通过的）
    available := make([]*BackendNode, 0, len(lb.nodes))
    for _, n := range lb.nodes {
        if n.CB.Allow() {
            available = append(available, n)
        }
    }
    if len(available) == 0 {
        // 全部熔断，返回第一个节点供调用方执行熔断降级与指标上报
        return lb.nodes[0]
    }
    if len(available) == 1 {
        return available[0]
    }

    // 随机选两个（确保 i ≠ j）
    i := rand.IntN(len(available))
    j := rand.IntN(len(available))
    for j == i {
        j = rand.IntN(len(available))
    }

    a, b := available[i], available[j]
    // 选择负载较低的（在途请求 * EWMA 延迟）
    scoreA := float64(a.InFlight.Load()+1) * math.Max(a.GetEWMA(), 0.001)
    scoreB := float64(b.InFlight.Load()+1) * math.Max(b.GetEWMA(), 0.001)

    if scoreA <= scoreB {
        return a
    }
    return b
}
```

**关键设计决策解读**：
- **为何全部熔断时返回 `nodes[0]`**：不返回 nil 是为了让调用方（HTTP 代理 / gRPC 代理）能执行统一的错误处理和指标上报逻辑，而非在各处判空
- **`rand.IntN` 而非 `rand.Int`**：Go 1.22+ 的新 API，自动使用线程安全的随机源，无需加锁
- **EWMA 下限 0.001**：新节点 EWMA 初始为 0，若不做下限保护，`score = (InFlight+1) * 0 = 0`，新节点会获得不合理的优先选择权

**5 种调度策略**：

| 策略 | 方法 | 无锁化设计 | 适用场景 |
|---|---|---|---|
| `p2c` (默认) | `selectP2C()` | `rand.IntN` + 原子读 InFlight | 通用生产环境 |
| `round_robin` | `selectRoundRobin()` | `atomic.Int32` fetch-and-add | 后端同构场景 |
| `least_conn` | `selectLeastConn()` | 原子读 InFlight | 长连接场景 |
| `weighted_rr` | `selectWeightedRoundRobin()` | Nginx SWRR 平滑加权，`atomic.Int32` currentWeight | 异构后端按权重分配 |
| `weighted_random` | `selectWeightedRandom()` | 概率与 Weight 成正比 | 灰度/金丝雀发布 |

**Nginx SWRR 平滑加权轮询源码**：
```go
// pkg/gateway/balancer.go L265-289
// 算法：每轮所有节点 currentWeight += weight；
// 选取 currentWeight 最大的节点；
// 被选中节点 currentWeight -= totalWeight。
// 保证分配比例精确且分布均匀（不会出现连续集中分配到同一节点）。
func (lb *LoadBalancer) selectWeightedRoundRobin() *BackendNode {
    available := make([]*BackendNode, 0, len(lb.nodes))
    for _, n := range lb.nodes {
        if n.CB.Allow() {
            available = append(available, n)
        }
    }
    if len(available) == 0 {
        return lb.nodes[0]
    }

    totalWeight := int32(0)
    var best *BackendNode
    bestCW := int32(-1 << 31) // min int32
    for _, n := range available {
        cw := n.currentWeight.Add(int32(n.Weight))  // 原子加
        totalWeight += int32(n.Weight)
        if cw > bestCW {
            bestCW = cw
            best = n
        }
    }
    best.currentWeight.Add(-totalWeight)  // 被选中节点减去总权重
    return best
}
```

#### 1.2.2 BackendNode 数据模型

**源码位置**：`pkg/gateway/balancer.go` (L26-42)

```go
// BackendNode 后端节点
type BackendNode struct {
    Address       string
    Weight        int
    currentWeight atomic.Int32            // Nginx SWRR 当前权重（原子操作）
    InFlight      atomic.Int64            // 当前在途请求数（原子操作，与 EWMA 锁分离）
    EWMA          float64                 // 指数移动加权平均延迟
    CB            *circuitbreaker.Breaker // 熔断器
    eWMAMu        sync.Mutex              // 仅保护 EWMA 字段

    // 反向代理实例与节点生命周期绑定：随节点惰性创建、随节点回收即释放。
    proxyOnce sync.Once
    proxy     *httputil.ReverseProxy
    proxyErr  error
}
```

**设计亮点**：
- `InFlight` 使用 `atomic.Int64`，`EWMA` 使用独立 `eWMAMu` 互斥量 — 二者锁分离，高频 InFlight 原子操作不会阻塞 EWMA 更新
- `proxyOnce` 实现惰性创建：反向代理实例随节点首次使用时构建，随节点回收即释放，取代了早期「全局 sync.Map + 后台 TTL 清理 goroutine」方案
- `DecrementInFlight()` 使用 CAS 循环（CompareAndSwap）而非简单 Add(-1)，防止并发下 InFlight 变为负数

**EWMA 更新源码**：
```go
// pkg/gateway/balancer.go L330-335
// UpdateEWMA 更新节点 EWMA 延迟（独立 eWMAMu，不与 InFlight 竞争）
func (n *BackendNode) UpdateEWMA(latency time.Duration, alpha float64) {
    n.eWMAMu.Lock()
    defer n.eWMAMu.Unlock()
    n.EWMA = alpha*float64(latency) + (1-alpha)*n.EWMA
}

// GetEWMA 安全读取节点 EWMA 延迟
func (n *BackendNode) GetEWMA() float64 {
    n.eWMAMu.Lock()
    defer n.eWMAMu.Unlock()
    return n.EWMA
}
```

#### 1.2.3 BufferPool 零分配机制

**源码位置**：`pkg/gateway/balancer.go` (L54-87)

```go
// byteBufferPool 实现 httputil.BufferPool 接口
type byteBufferPool struct {
    pool sync.Pool
}

func newByteBufferPool() *byteBufferPool {
    return &byteBufferPool{
        pool: sync.Pool{
            New: func() any {
                b := make([]byte, 32*1024)  // 32KB 预分配
                return &b
            },
        },
    }
}

func (p *byteBufferPool) Get() []byte {
    return *p.pool.Get().(*[]byte)
}

func (p *byteBufferPool) Put(b []byte) {
    // 仅回收 cap >= 32KB 的缓冲区（防止小缓冲区污染池）
    if cap(b) >= 32*1024 {
        p.pool.Put(&b)
    }
}

// 全局共享实例
var (
    globalBufferPool = newByteBufferPool()
    sharedTransport  = &http.Transport{
        MaxIdleConns:        2048,   // 全局最大空闲连接数
        MaxIdleConnsPerHost: 256,    // 单后端最大空闲连接
        IdleConnTimeout:     90 * time.Second,
        DisableCompression:  false,
    }
)
```

**共享传输层**（`sharedTransport`）：
- `MaxIdleConns: 2048` — 全局最大空闲连接数
- `MaxIdleConnsPerHost: 256` — 单后端最大空闲连接
- `IdleConnTimeout: 90s` — 空闲连接超时回收

**关键关注点**：
- `sync.Pool` 在 GC 时会清空所有池对象 — 高 GC 压力下可能频繁重建缓冲区
- `Put()` 的 `cap(b) >= 32*1024` 检查防止被缩容的 slice 污染池
- 所有代理共享同一个 `sharedTransport`，避免每个代理独立维护连接池

**反向代理惰性创建**：
```go
// pkg/gateway/balancer.go L93-117
func (n *BackendNode) ReverseProxy(metrics MetricsRecorder) (*httputil.ReverseProxy, error) {
    n.proxyOnce.Do(func() {
        target, err := url.Parse(fmt.Sprintf("http://%s", n.Address))
        if err != nil {
            n.proxyErr = err
            return
        }

        proxy := httputil.NewSingleHostReverseProxy(target)
        proxy.Transport = sharedTransport   // 共享连接池
        proxy.BufferPool = globalBufferPool // 共享缓冲区
        proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
            n.CB.RecordFailure()
            if metrics != nil {
                metrics.SetCircuitBreakerState(n.Address, n.CB.StateString())
                metrics.RecordForwarded(n.Address, http.StatusBadGateway)
            }
            w.Header().Set("Content-Type", "application/json")
            w.WriteHeader(http.StatusBadGateway)
            fmt.Fprintf(w, `{"code":"BAD_GATEWAY","message":"后端 %s 不可达",...}`, n.Address)
        }
        n.proxy = proxy
    })
    return n.proxy, n.proxyErr
}
```

#### 1.2.4 三态熔断器

**源码位置**：`pkg/circuitbreaker/circuitbreaker.go` (L39-141)

**状态机转换图**：
```
                    连续失败 >= threshold
    ┌──────────┐  ──────────────────────▶  ┌──────────┐
    │  Closed  │                           │   Open   │
    │ (正常)   │  ◀──────────────────────  │ (熔断)   │
    └──────────┘    冷却期已过              └──────────┘
         ▲                                    │
         │    HalfOpen 探测成功 >= halfOpenMax  │ 冷却期已过
         │  ◀─────────────────────────────────│
         │                                    ▼
         │                              ┌──────────┐
         └──────────────────────────────│ HalfOpen │
            HalfOpen 任何失败 → Open     │ (探测)   │
                                        └──────────┘
```

**完整数据结构**：
```go
// pkg/circuitbreaker/circuitbreaker.go L44-53
type Breaker struct {
    state       State         // 当前熔断状态（Closed / Open / HalfOpen）
    failures    int           // 连续失败计数（Closed 状态下累计）
    successes   int           // 探测成功计数（HalfOpen 状态下累计）
    threshold   int           // 触发熔断的连续失败次数阈值
    halfOpenMax int           // HalfOpen 状态下允许放行的最大探测请求数
    openedAt    time.Time     // 熔断开启时间戳（用于计算冷却期是否已过）
    cooldown    time.Duration // Open 状态最短持续时间（冷却期）
    mu          sync.Mutex    // 保护所有状态字段的互斥锁
}
```

**核心方法 `Allow()` 完整实现**：
```go
// pkg/circuitbreaker/circuitbreaker.go L78-96
func (b *Breaker) Allow() bool {
    b.mu.Lock()
    defer b.mu.Unlock()

    switch b.state {
    case StateClosed:
        return true  // 正常状态，始终放行
    case StateOpen:
        // 冷却期已过 → 自动降级为 HalfOpen，放行探测请求
        if time.Since(b.openedAt) >= b.cooldown {
            b.state = StateHalfOpen
            b.successes = 0
            return true
        }
        return false  // 冷却期未满，拒绝请求
    case StateHalfOpen:
        return b.successes < b.halfOpenMax  // 最多放行 halfOpenMax 个探测
    }
    return true
}
```

**关键参数**：
- `threshold`：触发熔断的连续失败次数（默认 5）
- `cooldown`：Open 状态最短持续时间（默认 30s）
- `halfOpenMax`：HalfOpen 状态最大探测请求数（硬编码 3）

**并发安全**：所有方法使用 `sync.Mutex` 保护，`Allow()` 在 Open 状态下同时检查冷却期并自动转换为 HalfOpen。

**Review 关注问题**：
- `halfOpenMax` 硬编码为 3，是否需要配置化？
- 冷却时间 `cooldown` 默认 30s 是否适合所有后端场景？
- `RecordFailure()` 中 `openedAt` 在每次失败时都更新（L123），而非仅在状态转换时更新 — 这是否影响冷却期计算？

#### 1.2.5 HTTP 反向代理处理器

**源码位置**：`engine-go/internal/gateway/http_proxy.go` (L19-77)

**完整请求处理流程**：
```go
// engine-go/internal/gateway/http_proxy.go L19-77
func NewHTTPProxyHandler(lb *LoadBalancer, metrics *observability.GatewayMetrics) gin.HandlerFunc {
    return func(c *gin.Context) {
        // 1. 选择后端节点（P2C-EWMA 或其他策略）
        node := lb.SelectNode()
        if node == nil {
            middleware.AbortWithError(c, http.StatusServiceUnavailable,
                "SERVICE_UNAVAILABLE", "无可用后端节点", "all backends exhausted")
            return
        }

        // 2. 检查熔断器
        if !node.CB.Allow() {
            middleware.AbortWithError(c, http.StatusServiceUnavailable,
                "CIRCUIT_OPEN", fmt.Sprintf("后端 %s 熔断器开启", node.Address), ...)
            return
        }

        // 3. 在途计数（defer 确保请求结束后递减）
        node.IncrementInFlight()
        defer node.DecrementInFlight()

        // 4. 获取节点绑定的反向代理（惰性创建，内置 BufferPool + 共享连接池）
        proxy, err := node.ReverseProxy(metrics)
        if err != nil {
            node.CB.RecordFailure()
            return
        }

        // 5. 执行代理并记录延迟
        start := time.Now()
        proxy.ServeHTTP(c.Writer, c.Request)
        latency := time.Since(start)

        // 6. 更新 EWMA（alpha=0.3）
        node.UpdateEWMA(latency, 0.3)

        // 7. 根据响应状态更新熔断器
        if c.Writer.Status() < 500 {
            node.CB.RecordSuccess()
        } else {
            node.CB.RecordFailure()
        }

        // 8. 上报 Prometheus 指标
        if metrics != nil {
            metrics.SetBackendEWMALatency(node.Address, float64(latency.Seconds()))
            metrics.SetCircuitBreakerState(node.Address, node.CB.StateString())
            metrics.RecordForwarded(node.Address, c.Writer.Status())
        }
    }
}
```

**执行时序图**：
```
客户端请求 ──▶ Gin Router
                │
                ▼
        ┌─ SelectNode() ──▶ P2C-EWMA 选择最优节点
        │
        ├─ CB.Allow()? ──NO──▶ 返回 503 CIRCUIT_OPEN
        │   │
        │  YES
        │   │
        ├─ IncrementInFlight()
        │
        ├─ ReverseProxy() ──▶ 获取/创建代理实例
        │
        ├─ proxy.ServeHTTP() ──▶ 转发到后端
        │
        ├─ UpdateEWMA(latency, 0.3)
        │
        ├─ CB.RecordSuccess/Failure()
        │
        ├─ 上报 Prometheus 指标
        │
        └─ DecrementInFlight() (defer)
```

#### 1.2.6 gRPC 透明流式代理

**源码位置**：`engine-go/internal/gateway/grpc_proxy.go` (L59-310)

**核心架构**：
```
客户端 ──gRPC Stream──▶ Gateway (UnknownServiceHandler)
                           │
                    ┌──────┴──────┐
                    │ RawCodec    │  ← 零编解码字节透传
                    │ P2C-EWMA    │  ← 选择最优后端
                    │ ConnPool    │  ← 连接池 (max 256)
                    └──────┬──────┘
                           │
                    双向零拷贝流转发
                    ┌──────┴──────┐
                    │ 客户端→后端  │  goroutine 1
                    │ 后端→客户端  │  goroutine 2
                    └─────────────┘
```

**RawCodec 设计**（L33-53）：
```go
// rawCodec 实现 grpc.encoding.Codec 接口，
// 直接透传原始字节而不做 marshal/unmarshal。
type rawCodec struct{}

func (rawCodec) Marshal(v interface{}) ([]byte, error) {
    if b, ok := v.(*[]byte); ok {
        return *b, nil  // 直接返回原始字节
    }
    return nil, fmt.Errorf("rawCodec: unsupported type %T", v)
}

func (rawCodec) Unmarshal(data []byte, v interface{}) error {
    if b, ok := v.(*[]byte); ok {
        *b = data  // 直接赋值，零拷贝
        return nil
    }
    return fmt.Errorf("rawCodec: unsupported type %T", v)
}

func (rawCodec) Name() string { return "raw" }
```

- 避免「先反序列化再序列化」的双重开销
- 通过 `grpc.ForceCodec(rawCodec{})` 强制使用

**连接池管理**（L82-122）：
```go
// getOrCreateConn 双重检查锁 + 健康检查
func (g *GrpcProxyServer) getOrCreateConn(addr string) (*grpc.ClientConn, error) {
    // 第一次检查（读锁，快速路径）
    g.connPoolMu.RLock()
    conn, ok := g.connPool[addr]
    g.connPoolMu.RUnlock()
    if ok && isConnReady(conn) {
        return conn, nil
    }

    // 加写锁，双重检查
    g.connPoolMu.Lock()
    defer g.connPoolMu.Unlock()
    conn, ok = g.connPool[addr]
    if ok && isConnReady(conn) {
        return conn, nil
    }

    // 连接池大小限制
    if len(g.connPool) >= g.maxPoolSize {
        return nil, fmt.Errorf("connection pool full (max %d)", g.maxPoolSize)
    }

    conn, err := grpc.DialContext(ctx, addr,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})),
    )
    g.connPool[addr] = conn
    return conn, nil
}
```

- 双重检查锁（Double-Check Locking）模式
- `maxPoolSize: 256` — 防止后端地址动态变化时内存泄漏
- `isConnReady()` 将 IDLE/READY/CONNECTING 均视为可用（连接池应复用而非反复创建）
- 仅 TRANSIENT_FAILURE/SHUTDOWN 视为不可用并触发重建

**双向流转发**（L193-253）：
```go
// 两个 goroutine 分别处理客户端→后端和后端→客户端方向
errChan := make(chan error, 2)  // 容量 2，防止 goroutine 泄漏
streamCtx, streamCancel := context.WithCancel(ctx)
defer streamCancel()

// 客户端 → 后端
go func() {
    for {
        select {
        case <-streamCtx.Done():
            errChan <- nil
            return
        default:
        }
        var frame []byte
        if err := serverStream.RecvMsg(&frame); err != nil {
            if err == io.EOF {
                _ = clientStream.CloseSend()
                errChan <- nil
                return
            }
            errChan <- err
            return
        }
        if err := clientStream.SendMsg(&frame); err != nil {
            errChan <- err
            return
        }
    }
}()

// 后端 → 客户端（对称实现，略）
```

- `streamCtx` + `streamCancel` 确保任一方向出错时两个 goroutine 都能退出
- `errChan` 容量为 2，防止 goroutine 泄漏
- 第一个 goroutine 退出后调用 `streamCancel()` 通知另一个退出

**Review 关注问题**：
- 连接池 Max 256 在高并发场景下是否足够？
- gRPC 代理使用 `insecure.NewCredentials()` — 后端连接是否应升级为 mTLS？
- 双向转发中 `RecvMsg(&frame)` 的 `frame` 是 `[]byte` 类型 — 大消息场景下的内存占用

#### 1.2.7 东西向 mTLS 回源 TLS

**源码位置**：`engine-go/internal/gateway/backend_tls.go` (L24-65)

**TLS 构建流程**：
1. 加载内部 CA 证书 → 构建 `x509.CertPool`（验证后端证书）
2. 加载网关客户端证书/密钥 → `tls.X509KeyPair`（后端验证网关身份）
3. 构建 `tls.Config`，强制 TLS 1.3 最低版本

**完整源码**：
```go
// engine-go/internal/gateway/backend_tls.go L24-46
func BuildBackendTLSConfig(caCertPath, clientCertPath, clientKeyPath string) (*tls.Config, error) {
    // 1. 加载 CA 证书
    caCert, err := os.ReadFile(caCertPath)
    if err != nil {
        return nil, fmt.Errorf("read CA cert %s: %w", caCertPath, err)
    }
    caCertPool := x509.NewCertPool()
    if !caCertPool.AppendCertsFromPEM(caCert) {
        return nil, fmt.Errorf("failed to parse CA cert %s", caCertPath)
    }

    // 2. 加载客户端证书（网关自身的身份凭证）
    clientCert, err := tls.LoadX509KeyPair(clientCertPath, clientKeyPath)
    if err != nil {
        return nil, fmt.Errorf("load client cert: %w", err)
    }

    // 3. 构建 TLS 配置
    return &tls.Config{
        Certificates: []tls.Certificate{clientCert},  // 网关客户端证书
        RootCAs:      caCertPool,                      // 用于验证后端证书
        MinVersion:   tls.VersionTLS13,                // 强制 TLS 1.3
    }, nil
}
```

**三种配置模式**：
- `BuildBackendTLSConfig`：标准 mTLS（TLS 1.3）
- `BuildBackendTLSConfigWithMinVersion`：支持降级到 TLS 1.2
- `BuildInsecureBackendTLSConfig`：仅加密不验证（开发/测试用）

### 1.3 Day 1-2 Review 方法

1. **先读测试**：`http_proxy_test.go` → `grpc_proxy_test.go` → `balancer_test.go`，理解预期行为
2. **再读实现**：对照测试用例理解边界条件
3. **画图理解**：画出 P2C 选择流程、熔断器状态机、gRPC 双向流转发时序图
4. **标记疑问**：对不理解的地方添加 `// REVIEW:` 注释

#### 一周计划总览（重新分配）

| 天 | 主题 | 对应章节 | 核心产出 |
|---|---|---|---|
| Day 1 | 前置知识 + 网关负载均衡器 | 第 1 章 P0-P3 + 第 2 章 §2.1-2.2 | P2C-EWMA 算法笔记、5 种调度策略对比 |
| Day 2 | gRPC 透明代理 + 熔断器 + BufferPool | 第 2 章剩余 + 第 7 章 | 双向流时序图、RawCodec 设计分析 |
| Day 3 | 安全体系：认证/权限/限流 | 第 3 章 §3.1-3.4 | 常量时间认证分析、32 分片限流性能分析 |
| Day 4 | 安全体系：合规增强 + 漏洞案例 | 第 4 章 + §3.5 | 7 项合规修复理解、SEC-09~13 案例 |
| Day 5 | 可观测性 + 部署运维 + 总结 | 第 5 章 + 第 8 章 + 交付物 | RED 指标验证、Prometheus 告警规则、交付物汇总 |

### 1.4 网关架构深度解析

#### 1.4.1 网关在整体架构中的位置

```
                    外部客户端 / 其他微服务
                           │
                           ▼
              ┌─────────────────────────┐
              │   privshield-gateway    │  ← 网关进程 (:8000 REST + :50000 gRPC)
              │                         │
              │  ┌───────────────────┐  │
              │  │ 负载均衡器 (P2C)  │  │
              │  │ 熔断器 (per-node) │  │
              │  │ BufferPool        │  │
              │  └─────────┬─────────┘  │
              └────────────┼────────────┘
                           │
              ┌────────────┼────────────┐
              │            │            │
              ▼            ▼            ▼
        ┌──────────┐ ┌──────────┐ ┌──────────┐
        │ Agent-1  │ │ Agent-2  │ │ Agent-3  │  ← 引擎进程 (:8079 REST + :50051 gRPC)
        │ :8079    │ │ :8079    │ │ :8079    │
        └──────────┘ └──────────┘ └──────────┘
```

**网关与引擎的职责分离**：
- **网关（Gateway）**：纯粹的流量治理层，不做任何隐私计算。负责负载均衡、熔断、TLS 终止、指标采集
- **引擎（Agent）**：隐私计算层，执行脱敏、差分隐私、K-匿名等原语。通过 sidecar 模式部署在每个业务服务旁

这种分离的好处：
1. 网关可以独立扩缩容，不受隐私计算资源需求影响
2. 引擎可以就近部署，减少隐私数据传输距离
3. 网关升级不影响隐私计算逻辑，反之亦然

#### 1.4.2 HTTP 代理请求完整生命周期

以一次 POST `/v1/privacy/mask` 请求为例，完整经过的组件：

```
1. 客户端发送 HTTP POST /v1/privacy/mask
   │
2. Gin Router 匹配路由
   │
3. 中间件链执行（安全头 → 追踪 → 限流 → 认证）
   │
4. NewHTTPProxyHandler 被调用
   │
5. lb.SelectNode() → P2C-EWMA 选择最优后端
   │  ├── 收集熔断器允许的节点
   │  ├── 随机选两个
   │  └── 比较 score = (InFlight+1) × max(EWMA, 0.001)
   │
6. node.CB.Allow() → 检查熔断器
   │
7. node.IncrementInFlight() → 原子计数 +1
   │
8. node.ReverseProxy(metrics) → 获取/创建反向代理
   │  ├── proxyOnce.Do() 确保只创建一次
   │  ├── Transport = sharedTransport（共享连接池）
   │  └── BufferPool = globalBufferPool（32KB 缓冲区池）
   │
9. proxy.ServeHTTP(c.Writer, c.Request) → 转发到后端
   │  ├── 从 sharedTransport 获取空闲连接
   │  ├── 从 globalBufferPool 获取 32KB 缓冲区
   │  ├── 拷贝请求体到后端连接
   │  ├── 等待后端响应
   │  ├── 拷贝响应体到客户端
   │  └── 归还缓冲区和连接
   │
10. node.UpdateEWMA(latency, 0.3) → 更新延迟指标
    │
11. node.CB.RecordSuccess/Failure() → 更新熔断器
    │
12. metrics 上报 → Prometheus 指标更新
    │
13. node.DecrementInFlight() → defer 原子计数 -1
```

#### 1.4.3 gRPC 代理与 HTTP 代理的关键差异

| 维度 | HTTP 代理 | gRPC 代理 |
|---|---|---|
| **协议** | HTTP/1.1 或 HTTP/2 | gRPC (HTTP/2) |
| **代理方式** | `httputil.ReverseProxy` 标准库 | 自定义双向流转发 |
| **编解码** | 无需编解码（透传 body） | `rawCodec` 零编解码字节透传 |
| **连接管理** | `sharedTransport` 连接池 | `connPool` map + 双重检查锁 |
| **流转发** | 无需（请求-响应模式） | 两个 goroutine 双向流 |
| **服务发现** | 静态后端列表 | 静态后端列表 + UnknownServiceHandler |
| **EWMA alpha** | 0.3 | 0.2（更平滑） |

**为何 gRPC 代理不使用标准反向代理**：
- gRPC 使用 HTTP/2 多路复用，一个连接上可以有多个流
- gRPC 的消息是 protobuf 编码的帧，不是简单的 HTTP body
- 需要支持双向流（Bidirectional Streaming），标准反向代理不支持
- `rawCodec` 避免了「反序列化 → 重新序列化」的双重开销

#### 1.4.4 熔断器设计决策深度分析

**决策 1：为何 `halfOpenMax` 硬编码为 3？**

```
探测次数太少（如 1）：单次网络抖动就可能误判后端未恢复
探测次数太多（如 10）：恢复期间过多请求被放入不健康后端
3 次是一个工程经验值：平衡了误判率和恢复速度
```

**决策 2：`RecordFailure()` 每次更新 `openedAt` 的影响**

```go
// circuitbreaker.go L118-133
func (b *Breaker) RecordFailure() {
    b.mu.Lock()
    defer b.mu.Unlock()

    b.failures++
    b.openedAt = time.Now()  // ← 每次失败都更新

    switch b.state {
    case StateClosed:
        if b.failures >= b.threshold {
            b.state = StateOpen
        }
    case StateHalfOpen:
        b.state = StateOpen  // HalfOpen 任何失败立即回退
    }
}
```

这意味着在 Closed 状态下，即使还没触发熔断，`openedAt` 也在不断更新。如果进入 Open 状态，冷却期是从最后一次失败开始计算的，而非从第一次触发熔断开始。这实际上是更保守的策略：确保后端有足够时间恢复。

**决策 3：全部熔断时返回 `nodes[0]` 而非 nil**

```go
if len(available) == 0 {
    return lb.nodes[0]  // 而非 return nil
}
```

这是一个「宁可有错、不可崩溃」的设计：
- 返回 nil 需要每个调用方都处理 nil 情况
- 返回 `nodes[0]` 让调用方正常执行，熔断器会拒绝请求并返回 503
- 错误处理逻辑统一在代理层，而非分散在选择层

#### 1.4.5 BufferPool 性能分析

**为何需要 BufferPool**：

`httputil.ReverseProxy` 在转发请求时，需要将请求体从客户端拷贝到后端。如果不使用 BufferPool，每次请求都需要分配一个新的缓冲区：

```go
// 不使用 BufferPool（每次分配新内存）
buf := make([]byte, 32*1024)  // GC 压力
io.CopyBuffer(dst, src, buf)

// 使用 BufferPool（复用已有缓冲区）
buf := pool.Get()  // 从池中取
defer pool.Put(buf) // 用完归还
io.CopyBuffer(dst, src, buf)
```

**GC 影响分析**：
- `sync.Pool` 在每次 GC 时会清空所有池对象
- Go 的 GC 触发频率取决于堆增长率，通常在堆翻倍时触发
- 高 QPS 场景下，GC 可能每秒触发多次，导致缓冲区频繁重建
- 但 `New` 函数只是 `make([]byte, 32*1024)`，开销极低（~1μs）
- 总体收益远大于成本：避免了大量短生命周期对象

**`cap(b) >= 32*1024` 检查的意义**：

```go
func (p *byteBufferPool) Put(b []byte) {
    if cap(b) >= 32*1024 {  // 只回收容量足够的缓冲区
        p.pool.Put(&b)
    }
    // 小缓冲区直接丢弃，让 GC 回收
}
```

如果不检查，可能被缩容的 slice（如 `b[:100]`）污染池，导致后续取出的缓冲区容量不足，引发代理错误。

#### 1.4.6 共享 Transport 的连接池策略

```go
sharedTransport = &http.Transport{
    MaxIdleConns:        2048,              // 全局最大空闲连接
    MaxIdleConnsPerHost: 256,               // 单后端最大空闲连接
    IdleConnTimeout:     90 * time.Second,  // 空闲超时
    DisableCompression:  false,             // 启用压缩
}
```

**参数选择依据**：
- `MaxIdleConns: 2048`：假设 8 个后端节点，每个 256 连接，总共 2048
- `MaxIdleConnsPerHost: 256`：单个后端在突发流量时可能需要的并发连接数
- `IdleConnTimeout: 90s`：平衡连接复用率和资源占用。太短导致频繁重建，太长浪费文件描述符
- `DisableCompression: false`：启用 gzip 压缩，减少内网带宽占用

**潜在问题**：
- 所有代理共享同一个 Transport，如果某个后端响应极慢，可能占用大量连接，影响其他后端
- 解决方案：可以为每个节点创建独立的 Transport，但会增加内存开销

---

## 第 3 章：Day 3-4 安全体系 Review（mTLS、认证、权限）

### 2.1 审查文件清单

| 文件路径 | 行数 | 核心职责 |
|---|:---:|---|
| `engine-go/internal/security/config.go` | 143 | 安全配置加载（环境变量 → Settings 单例），使用共享 `LoadAPIKeysFromEnv` |
| `engine-go/internal/security/auth.go` | 76 | 认证中间件桥接 + 安全头 + 限流中间件 |
| `engine-go/internal/security/identity.go` | 28 | 身份类型别名 + 权限映射函数 |
| `engine-go/internal/security/whitelist.go` | 226 | mTLS CN 白名单管理器（YAML 热重载） |
| `pkg/auth/identity.go` | 237 | Identity 模型 + Scope 权限模型 + REST/gRPC 权限映射 + `ParseAPIKeysEnv` + `ServiceHubPermissionForPath` |
| `pkg/auth/middleware.go` | 178 | API Key 认证中间件 + 常量时间查找 |
| `pkg/auth/settings.go` | ~30 | KeyConfig + Settings 数据结构 |
| `pkg/tlsutil/whitelist.go` | 271 | 动态 mTLS CN 白名单（5s 轮询热重载 + Scope 匹配） |
| `pkg/tlsutil/grpc_interceptor.go` | 134 | gRPC mTLS CN 白名单拦截器（一元 + 流式） |
| `pkg/middleware/ratelimit.go` | 314 | 32 分片令牌桶限流 + DDoS 防护 |
| `pkg/middleware/auth.go` | 192 | 基础 API Key 认证 + AuthWithRoles 读写分离 |
| `pkg/middleware/trace.go` | 53 | 分布式追踪上下文传播中间件 |
| `services/service-hub/internal/handlers/handlers.go` | ~300 | `scopeAuthMiddleware` 双模式鉴权 + `constantTimeLookupKeys` |

### 2.2 核心知识点详解

#### 2.2.1 mTLS CN 白名单热重载机制

**两套实现并存**（须理解差异）：

| 维度 | `engine-go/internal/security/whitelist.go` | `pkg/tlsutil/whitelist.go` |
|---|---|---|
| **定位** | engine-go Agent 专用（REST 侧） | 共享基础库（gRPC 侧） |
| **重载方式** | 请求驱动被动检查（`checkReload`） | 后台 5s 轮询主动检查（`poll`） |
| **YAML 格式** | `entries: [{cn, scopes}]` | `clients: [{cn, allowed_scopes}]` + 兼容 `entries` |
| **Scope 模型** | 简单 scope 列表 | 支持通配符 `/ServiceHub/*` 前缀匹配 |
| **停机能力** | 无（无后台协程） | `Close()` 优雅停止轮询协程 |

**engine-go 侧 WhitelistManager**（L34-226）：
- **两阶段提交**：先解析到临时 `newCache` map，成功后在 `mu.Lock()` 临界区内原子交换 `m.cache = newCache`
- **请求驱动重载**：`GetEntry()` 每次调用前执行 `checkReload()` → `os.Stat()` 检查 mtime → 若变更则触发 `load()`
- **节流机制**：无显式节流（每次请求都 `os.Stat`），但 `os.Stat` 是系统调用级别操作，开销极低
- **模块级单例**：`GetWhitelistManager()` 使用 `sync.Once` 保证全局唯一实例

**pkg 侧 DynamicWhitelist**（L86-271）：

```go
// pkg/tlsutil/whitelist.go L86-95
type DynamicWhitelist struct {
    mu      sync.RWMutex
    clients map[string][]string // CN → allowed scopes 切片
    path    string              // 配置文件物理路径

    stopCh  chan struct{}  // 停机信号
    stopped bool
    stopMu  sync.Mutex
}
```

- **后台轮询**：独立 goroutine 每 5s 通过 `os.Stat` 检测 mtime 变更
- **双格式兼容**：优先解析 `clients` 键（设计标准），回退到 `entries` 键（历史格式）
- **Scope 匹配规则**（`CheckScope` L227-242）：
  1. 全局通配符 `"*"` → 允许所有方法
  2. 精确全名匹配 → `/PrivacyService/Process`
  3. 前缀通配符 → `/AuditLog/*` 匹配所有 `/AuditLog/` 前缀方法
- **优雅停机**：`Close()` 使用 `stopMu` 互斥量保护，支持幂等调用

**白名单配置文件示例**（`config/mtls-whitelist.yaml`）：
```yaml
version: "1.0"
clients:
  - cn: "service-hub.privshield.internal"
    allowed_scopes:
      - "/PrivacyService/Process"
      - "/AuditLog/*"
    role: "orchestrator"
    description: "数据服务调度中枢核心客户端"
    enabled: true
  - cn: "bff-go.privshield.internal"
    allowed_scopes: ["*"]
    role: "gateway"
    enabled: true
```

#### 2.2.2 API Key 常量时间认证

**源码位置**：`pkg/auth/middleware.go` `ConstantTimeLookup()` (L55-75)

**为何必须常量时间比较**：
- 标准 `==` 比较在发现第一个不同字符时即返回，攻击者可通过测量响应时间逐字符猜解密钥
- `subtle.ConstantTimeCompare` 始终比较所有字节，耗时与输入长度成正比而非匹配位置

**完整实现**：
```go
// pkg/auth/middleware.go L55-75
func ConstantTimeLookup(keys map[string]*KeyConfig, token string) *KeyConfig {
    if len(keys) == 0 {
        return nil
    }
    // 1. 排序 key 确保确定性迭代顺序（Go map 迭代顺序随机）
    sortedKeys := make([]string, 0, len(keys))
    for k := range keys {
        sortedKeys = append(sortedKeys, k)
    }
    sort.Strings(sortedKeys)

    // 2. 遍历全部 key 且始终比较所有 key（不提前返回）
    tokenBytes := []byte(token)
    var matched *KeyConfig
    for _, key := range sortedKeys {
        // subtle.ConstantTimeCompare 确保每次比较耗时恒定
        if subtle.ConstantTimeCompare([]byte(key), tokenBytes) == 1 {
            matched = keys[key]  // 记录但不 break，继续比较剩余 key
        }
    }
    return matched
}
```

**关键安全特性**：
- 排序 key 消除 map 迭代随机性带来的时序差异
- 不提前 break — 即使已找到匹配也继续比较所有 key，防止时序侧信道泄漏匹配位置
- 对内部和外部 key 存储分别执行常量时间查找（`authenticateAPIKey` L78-86）

**认证完整链路**：
```
HTTP 请求
    │
    ▼
AuthMiddleware(settings)
    │
    ├─ IsHealthPathOrMethod(path)? ──YES──▶ 注入健康探针身份，放行
    │
    ├─ !settings.AuthEnabled? ──YES──▶ 注入匿名身份，放行
    │
    ├─ ExtractBearerToken(Authorization header)
    │   └─ 空? ──YES──▶ 401 UNAUTHENTICATED
    │
    ├─ authenticateAPIKey(settings, token)
    │   ├─ ConstantTimeLookup(InternalKeys, token) ──命中──▶ Identity{internal}
    │   └─ ConstantTimeLookup(ExternalKeys, token) ──命中──▶ Identity{external}
    │   └─ 都未命中 ──▶ 401 UNAUTHENTICATED
    │
    ├─ PermissionForRESTPath(path) → requiredPerm
    │   └─ requiredPerm != "" && !identity.HasPermission(requiredPerm)?
    │       ──YES──▶ 403 FORBIDDEN
    │
    └─ c.Set(IdentityContextKey, identity) → c.Next()
```

#### 2.2.3 Scope-based 权限模型

**源码位置**：`pkg/auth/identity.go` (L1-236)

**Identity 结构**：
```go
type Identity struct {
    ServiceType string   // "internal" (高信任) | "external" (外部客户端)
    Name        string   // 服务/账户名称
    Scopes      []string // 权限列表，["*"] 表示完全访问
}
```

**HasPermission 实现**：
```go
// pkg/auth/identity.go L23-30
func (id *Identity) HasPermission(permission string) bool {
    for _, s := range id.Scopes {
        if s == "*" || s == permission {
            return true
        }
    }
    return false
}
```

**REST 路径 → 权限映射**（`PermissionForRESTPath`）：

> **关键设计**：函数入口处执行路径归一化 `/api/v1/*` → `/v1/*`，确保别名路由与主路由共享同一权限映射，避免重复 case 分支。

| 路径前缀（归一化后） | 权限字符串 | 说明 |
|---|---|---|
| `/health`, `/livez`, `/readyz`, `/readyz/llm` | `health:read` | 健康探针 |
| `/v1/privacy/mask*` | `privacy:mask` | 掩码原语 |
| `/v1/privacy/hash` | `privacy:hash` | 国密哈希 |
| `/v1/privacy/dp/*`, `/v1/privacy/ldp/*` | `privacy:dp` | 差分隐私 / 本地 DP |
| `/v1/privacy/k_anonymize*` | `privacy:kano` | K-匿名 |
| `/v1/privacy/qol/*` | `privacy:qol` | 查询混淆 |
| `/v1/privacy/budget`, `/v1/privacy/budget/reset` | `privacy:budget` | 隐私预算查询与重置 |
| `/v1/privacy/profile/recommend` | `privacy:profile` | Profile 推荐 |
| `/v1/privacy/process_file` | `privacy:mask` | 文件脱敏 |
| `/v1/privacy/classify/*` | `classification:read` | 静态分类 |
| `/v1/dynclassification/profiles/reload` | `dynclassification:write` | 动态分类 Profile 重载 |
| `/v1/dynclassification/generate_profile` | `dynclassification:write` | 动态分类 Profile 生成 |
| `/v1/dynclassification*`（其他） | `dynclassification:read` | 动态分类读取（默认） |
| `/v1/agent*` | `agent:process` | Agent 流水线 |
| `/v1/medical*` | `medical:process` | 医疗数据管线 |
| `/v1/pipeline*` | `pipeline:process` | 通用管线 |
| `/v1/ops/*` | `ops:diagnostics` | 运维诊断 |
| `/debug/pprof*` | `ops:admin` | pprof 性能分析 |
| **根路径别名**（归一化后） | | |
| `/agent/process` | `agent:process` | 根路径直调别名 |
| `/medical/process` | `medical:process` | 根路径直调别名 |
| `/ops/diagnostics` | `ops:diagnostics` | 根路径直调别名 |
| `/privacy/process_file` | `privacy:mask` | 根路径直调别名 |
| **快捷别名路由**（`/api/v1/*` 归一化后去掉 `/privacy/` 段） | | |
| `/v1/mask*` | `privacy:mask` | 快捷别名 |
| `/v1/dp/*` | `privacy:dp` | 快捷别名 |
| `/v1/kano/*` | `privacy:kano` | 快捷别名 |
| `/v1/qol/*` | `privacy:qol` | 快捷别名 |
| `/v1/ldp/*` | `privacy:dp` | 快捷别名 |
| `/v1/classify`, `/v1/classify/batch` | `classification:read` | 快捷别名 |
| `/v1/hash/hmac` | `privacy:hash` | 快捷别名 |
| `/v1/budget`, `/v1/budget/reset` | `privacy:budget` | 快捷别名 |

**gRPC 方法 → 权限映射**（`PermissionForGRPCMethod` L132-168）：
```go
// pkg/auth/identity.go L141-163
mapping := map[string]string{
    "Mask": "privacy:mask", "MaskRecord": "privacy:mask",
    "MaskBatch": "privacy:mask", "MaskDataFrame": "privacy:mask",
    "DPCount": "privacy:dp", "DPSum": "privacy:dp", "DPMean": "privacy:dp",
    "DPHistogram": "privacy:dp", "DPChunkedCount": "privacy:dp",
    "KAnonymizeRecord": "privacy:kano", "KAnonymizeTable": "privacy:kano",
    "ObfuscateQuery": "privacy:qol",
    "ClassifyField": "classification:read",
    "DynClassify": "dynclassification:read",
    "Health": "health:read",
    // ... 共覆盖 44 个隐私原语
}
```

#### 2.2.3.1 Scope-based 鉴权完整请求生命周期（深度解析）

本节以一个具体请求为例，逐步拆解 Scope-based 鉴权从「环境变量配置」到「请求放行/拒绝」的 7 个阶段。理解这条完整链路是掌握 PrivShield 安全体系的核心。

##### 阶段总览

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    Scope-based 鉴权 7 阶段模型                          │
├─────────────────────────────────────────────────────────────────────────┤
│                                                                         │
│  ① 配置解析（启动时）                                                    │
│     ParseAPIKeysEnv("sk-abc:hub:hub:read;sk-xyz:admin:*")              │
│       → map[sk-abc]{Name:"hub",Scopes:["hub:read"]}                    │
│       → map[sk-xyz]{Name:"admin",Scopes:["*"]}                         │
│                                                                         │
│  ② Token 提取（每次请求）                                                │
│     Authorization: Bearer sk-abc  →  "sk-abc"                          │
│                                                                         │
│  ③ 常量时间查找（防时序攻击）                                             │
│     ConstantTimeLookup(keys, "sk-abc")  →  KeyConfig{Name:"hub",...}   │
│                                                                         │
│  ④ Identity 构造                                                        │
│     → Identity{ServiceType:"internal", Name:"hub", Scopes:["hub:read"]} │
│                                                                         │
│  ⑤ 路径 → 权限映射（含归一化）                                           │
│     PermissionForRESTPath("/api/v1/privacy/mask")                       │
│       → 归一化: "/api/v1/privacy/mask" → "/v1/privacy/mask"            │
│       → 匹配: HasPrefix("/v1/privacy/mask") → "privacy:mask"           │
│                                                                         │
│  ⑥ Scope 校验                                                          │
│     Identity.HasPermission("privacy:mask")                              │
│       → Scopes=["hub:read"] 中无 "*" 也无 "privacy:mask"               │
│       → false → 403 FORBIDDEN                                          │
│                                                                         │
│  ⑦ 上下文注入（放行时）                                                  │
│     c.Set("security_identity", identity) → c.Next()                    │
│       → 下游 Handler 可通过 GetIdentity(c) 获取身份做业务逻辑            │
│                                                                         │
└─────────────────────────────────────────────────────────────────────────┘
```

##### 阶段 ①：配置解析 — `ParseAPIKeysEnv`

**触发时机**：进程启动时，`loadSettings()` 从环境变量读取并解析一次。

**输入格式**：`token:name:scope1,scope2;token2:name2:scope3`

```
环境变量原始值:
  "sk-abc123:service-hub:hub:read,hub:dispatch;sk-readonly:monitor:hub:read;sk-admin:bff-go:*"

                        ↓ ParseAPIKeysEnv()

解析结果 (map[string]*KeyConfig):
  ┌────────────┬─────────────────┬──────────────────────────────┐
  │ Token      │ Name            │ Scopes                       │
  ├────────────┼─────────────────┼──────────────────────────────┤
  │ sk-abc123  │ service-hub     │ ["hub:read", "hub:dispatch"] │
  │ sk-readonly│ monitor         │ ["hub:read"]                 │
  │ sk-admin   │ bff-go          │ ["*"]                        │
  └────────────┴─────────────────┴──────────────────────────────┘
```

**解析管线**（5 步）：

```go
// 步骤 1：按 ";" 分割多 Key 条目
entries := strings.Split(raw, ";")
// ["sk-abc123:service-hub:hub:read,hub:dispatch", "sk-readonly:monitor:hub:read", "sk-admin:bff-go:*"]

// 步骤 2：每条按 ":" 分割为 token:name:scopes（SplitN 限制为 3 段，
//         因为 scope 值本身包含 ":"，如 "hub:read"）
parts := strings.SplitN(entry, ":", 3)
// ["sk-abc123", "service-hub", "hub:read,hub:dispatch"]

// 步骤 3：TrimSpace 防环境变量中的前后空格
token := strings.TrimSpace(parts[0])  // "sk-abc123"
name  := strings.TrimSpace(parts[1])  // "service-hub"

// 步骤 4：scopes 按 "," 分割
scopes := strings.Split(parts[2], ",")  // ["hub:read", "hub:dispatch"]

// 步骤 5：缺少 scopes 时默认 ["*"]（全部权限）
if len(scopes) == 0 { scopes = []string{"*"} }
```

> **关键细节**：`SplitN(entry, ":", 3)` 的 `3` 是刻意设计。因为 Scope 值本身包含冒号（如 `hub:read`），如果用 `Split(entry, ":")` 会把 `hub:read` 错误地拆成两段。`SplitN` 限制最多拆为 3 段，第三段保留完整的 `hub:read,hub:dispatch`。

**安全增强**：
- 空 token 丢弃：`if token == "" { continue }` — 防止 `":name:scope"` 这样的畸形条目注册空字符串 Key
- 空 name 丢弃：`if name == "" { continue }` — 防止无标识 Key 混入

##### 阶段 ②：Token 提取 — `ExtractBearerToken`

**触发时机**：每次 HTTP 请求到达 AuthMiddleware。

```go
// pkg/auth/middleware.go L44-50
func ExtractBearerToken(header string) string {
    parts := strings.Fields(header)
    // strings.Fields 按空白字符分割，自动处理多余空格
    if len(parts) == 2 && strings.EqualFold(parts[0], "bearer") {
        return parts[1]
    }
    return ""
}
```

**输入输出示例**：

| Authorization Header | 提取结果 | 说明 |
|---|---|---|
| `Bearer sk-abc123` | `sk-abc123` | 标准格式 |
| `bearer sk-abc123` | `sk-abc123` | `EqualFold` 大小写不敏感 |
| `Bearer  sk-abc123` | `sk-abc123` | `Fields` 自动处理多余空格 |
| `Basic dXNlcjpwYXNz` | `""` | 非 Bearer 方案，返回空 |
| `""` (空) | `""` | 无 Header |
| `Bearer` | `""` | 只有方案无 Token |

##### 阶段 ③：常量时间查找 — `ConstantTimeLookup`

**触发时机**：Token 提取成功后，在 Key 映射中查找匹配。

**为何不能用 `map[token]` 直接查找？**

```go
// ❌ 不安全：map 直接查找存在时序侧信道
func insecureLookup(keys map[string]*KeyConfig, token string) *KeyConfig {
    if k, ok := keys[token]; ok {  // 找到即返回，不同 Key 的响应时间不同
        return k
    }
    return nil
}

// 攻击者可以通过测量响应时间逐字符猜测 Token：
//   尝试 "a..." → 100ns（第一个字符就不匹配任何 Key）
//   尝试 "s..." → 150ns（第一个字符匹配了 "sk-..." 的 Key）
//   尝试 "sk-..." → 200ns（前 3 个字符匹配）
//   ...最终破解完整 Token
```

**安全实现**：

```go
// pkg/auth/middleware.go L55-75
func ConstantTimeLookup(keys map[string]*KeyConfig, token string) *KeyConfig {
    // 1. 排序 key：Go map 迭代顺序随机，排序消除时序差异
    sortedKeys := make([]string, 0, len(keys))
    for k := range keys { sortedKeys = append(sortedKeys, k) }
    sort.Strings(sortedKeys)

    // 2. 遍历全部 key，不提前 break
    tokenBytes := []byte(token)
    var matched *KeyConfig
    for _, key := range sortedKeys {
        // subtle.ConstantTimeCompare：无论在第几个字节不同，耗时都相同
        if subtle.ConstantTimeCompare([]byte(key), tokenBytes) == 1 {
            matched = keys[key]  // 记录但不 break，继续比较剩余 key
        }
    }
    return matched
    // 总耗时 = len(sortedKeys) × constant_time_compare
    // 无论 Token 是否匹配、匹配第几个 Key，耗时完全相同
}
```

**时序对比**：

```
不安全的 map 查找：
  Token 匹配第 1 个 Key → 比较 1 次 → 100ns → 返回
  Token 匹配第 3 个 Key → 比较 3 次 → 300ns → 返回
  Token 不匹配          → 比较 3 次 → 300ns → 返回 nil
  ↑ 攻击者可以区分「匹配」和「不匹配」

ConstantTimeLookup：
  Token 匹配第 1 个 Key → 比较 3 次（不提前 break）→ 300ns → 返回
  Token 匹配第 3 个 Key → 比较 3 次                 → 300ns → 返回
  Token 不匹配          → 比较 3 次                 → 300ns → 返回 nil
  ↑ 三种情况耗时完全相同，攻击者无法获取任何信息
```

##### 阶段 ④：Identity 构造 — `authenticateAPIKey`

**触发时机**：`ConstantTimeLookup` 返回匹配的 KeyConfig 后。

```go
// pkg/auth/middleware.go L78-86
func authenticateAPIKey(settings *Settings, token string) *Identity {
    // 先查 internal keys（高信任内部服务）
    if internal := ConstantTimeLookup(settings.InternalKeys, token); internal != nil {
        return &Identity{
            ServiceType: "internal",         // 标记为内部服务
            Name:        internal.Name,      // 如 "service-hub"
            Scopes:      internal.Scopes,    // 如 ["hub:read", "hub:dispatch"]
        }
    }
    // 再查 external keys（外部客户端）
    if external := ConstantTimeLookup(settings.ExternalKeys, token); external != nil {
        return &Identity{
            ServiceType: "external",         // 标记为外部客户端
            Name:        external.Name,
            Scopes:      external.Scopes,
        }
    }
    return nil  // 两个 Key 池都未命中 → 认证失败
}
```

**构造出的 Identity 示例**：

```
输入 Token: "sk-abc123"（配置在 InternalKeys 中）

→ Identity{
    ServiceType: "internal",
    Name:        "service-hub",
    Scopes:      ["hub:read", "hub:dispatch"],
  }

该 Identity 携带了：
  - 谁在调用？ → "service-hub"（内部服务）
  - 允许做什么？ → 只能执行 hub:read 和 hub:dispatch 权限的操作
```

##### 阶段 ⑤：路径 → 权限映射 — `PermissionForRESTPath`

**触发时机**：Identity 构造成功后，根据请求路径确定所需权限。

**路径归一化是核心**：

```
输入路径: "/api/v1/privacy/mask"

步骤 1：去除尾部斜杠
  "/api/v1/privacy/mask" → "/api/v1/privacy/mask"（无尾部斜杠，不变）

步骤 2：前缀归一化 /api/v1/* → /v1/*
  "/api/v1/privacy/mask" → "/v1/privacy/mask"
  （去掉 "/api" 前缀，使别名路由与主路由共享同一权限映射）

步骤 3：switch-case 匹配
  HasPrefix("/v1/privacy/mask", "/v1/privacy/mask") → true
  → 返回 "privacy:mask"
```

**归一化解决了什么问题？**

```
修复前（只有 /v1/* 匹配）：
  /v1/privacy/mask        → "privacy:mask" ✓ 有权限保护
  /api/v1/privacy/mask    → ""             ✗ 无权限保护（绕过！）
  /agent/process          → ""             ✗ 无权限保护（绕过！）

修复后（归一化 + 全量 case）：
  /v1/privacy/mask        → 归一化 → /v1/privacy/mask  → "privacy:mask" ✓
  /api/v1/privacy/mask    → 归一化 → /v1/privacy/mask  → "privacy:mask" ✓
  /agent/process          → 归一化 → /agent/process     → "agent:process" ✓
```

**更多归一化示例**：

| 原始路径 | 归一化后 | 匹配 case | 权限字符串 |
|---|---|---|---|
| `/api/v1/privacy/dp/count` | `/v1/privacy/dp/count` | `HasPrefix("/v1/privacy/dp/")` | `privacy:dp` |
| `/api/v1/mask` | `/v1/mask` | `HasPrefix("/v1/mask")` | `privacy:mask` |
| `/api/v1/ldp/randomized_response` | `/v1/ldp/randomized_response` | `HasPrefix("/v1/ldp/")` | `privacy:dp` |
| `/v1/dynclassification/profiles/reload` | 不变 | `== ".../profiles/reload"` | `dynclassification:write` |
| `/v1/dynclassification` | 不变 | `HasPrefix("/v1/dynclassification")` 默认 | `dynclassification:read` |
| `/api/hub/dispatch/` | `/api/hub/dispatch` | service-hub 单独处理 | `hub:dispatch` |

##### 阶段 ⑥：Scope 校验 — `HasPermission`

**触发时机**：路径映射返回所需权限后，与 Identity 的 Scopes 比对。

```go
// pkg/auth/identity.go L26-33
func (id *Identity) HasPermission(permission string) bool {
    for _, s := range id.Scopes {
        if s == "*" || s == permission {
            return true
        }
    }
    return false
}
```

**判定逻辑**：
1. 遍历 Identity 的 Scopes 列表
2. 如果遇到 `"*"` → 通配符，授予所有权限，立即返回 `true`
3. 如果遇到精确匹配 → 返回 `true`
4. 遍历完毕无匹配 → 返回 `false`

**具体场景演示**：

```
场景 A：service-hub 调用 engine-go 的掩码接口
  Identity: {Name: "service-hub", Scopes: ["privacy:mask", "privacy:dp"]}
  路径: POST /api/v1/privacy/mask
  所需权限: "privacy:mask"
  校验: "privacy:mask" ∈ ["privacy:mask", "privacy:dp"] → true → 200 OK ✓

场景 B：monitor 尝试调用差分隐私接口
  Identity: {Name: "monitor", Scopes: ["hub:read"]}
  路径: POST /api/v1/privacy/dp/count
  所需权限: "privacy:dp"
  校验: "privacy:dp" ∉ ["hub:read"] → false → 403 FORBIDDEN ✗

场景 C：admin（通配符）调用任意接口
  Identity: {Name: "bff-go", Scopes: ["*"]}
  路径: POST /api/v1/dynclassification/profiles/reload
  所需权限: "dynclassification:write"
  校验: "*" 匹配一切 → true → 200 OK ✓

场景 D：未映射路径（新端点忘记配权限）
  Identity: {Name: "client", Scopes: ["privacy:mask"]}
  路径: POST /v1/new_endpoint
  所需权限: ""（未映射，返回空串）
  校验: requiredPerm == "" → 跳过权限检查 → 200 OK
  ⚠️ 设计决策：未映射路径对所有已认证身份开放（需 Code Review 保障）
```

##### 阶段 ⑦：上下文注入与下游使用

**触发时机**：鉴权通过后，将 Identity 注入 Gin Context。

```go
// pkg/auth/middleware.go L127-128
c.Set(IdentityContextKey, identity)  // 存入 Context
c.Next()                              // 放行到下一层中间件/Handler
```

**下游使用方式**：

```go
// 在 Handler 中获取当前请求的 Identity
identity := auth.GetIdentity(c)

// 用途 1：日志记录（谁在调用）
slog.Info("mask request", "caller", identity.Name, "type", identity.ServiceType)

// 用途 2：限流 key 构造（按身份分片）
rateLimitKey := identity.ServiceType + ":" + identity.Name + ":" + path

// 用途 3：业务逻辑分支（内部服务 vs 外部客户端）
if identity.ServiceType == "internal" {
    // 内部服务可以访问更多诊断信息
}
```

##### 完整请求链路时序图

```
客户端                    AuthMiddleware                   PermissionForRESTPath
  │                            │                                  │
  │── POST /api/v1/privacy/mask ──▶│                              │
  │   Authorization: Bearer sk-abc │                              │
  │                            │                                  │
  │                     ① ExtractBearerToken                      │
  │                        → "sk-abc"                            │
  │                            │                                  │
  │                     ② ConstantTimeLookup(InternalKeys)        │
  │                        → KeyConfig{Name:"hub",               │
  │                           Scopes:["hub:read"]}               │
  │                            │                                  │
  │                     ③ 构造 Identity                           │
  │                        → {internal, "hub", ["hub:read"]}     │
  │                            │                                  │
  │                            │── "/api/v1/privacy/mask" ──────▶│
  │                            │                                  │ 归一化: /api/v1/ → /v1/
  │                            │                                  │ 匹配: → "privacy:mask"
  │                            │◀──── "privacy:mask" ────────────│
  │                            │                                  │
  │                     ④ HasPermission("privacy:mask")           │
  │                        ["hub:read"] 中无 "privacy:mask"      │
  │                        → false                               │
  │                            │                                  │
  │◀── 403 FORBIDDEN ────────│                                   │
  │    {"code":"FORBIDDEN",   │                                   │
  │     "message":"..."}      │                                   │
```

##### 安全属性总结

| 安全属性 | 实现机制 | 防御目标 |
|---|---|---|
| **防时序攻击** | `ConstantTimeLookup`：排序 key + 全量遍历 + `subtle.ConstantTimeCompare` | 逐字符猜测 Token |
| **防别名绕过** | `PermissionForRESTPath` 入口路径归一化 `/api/v1/*` → `/v1/*` | 通过别名路径跳过权限校验 |
| **防尾部斜杠绕过** | 去除尾部斜杠后再匹配 | `/api/hub/dispatch/` 绕过 `== "/api/hub/dispatch"` |
| **防空 Key 注入** | `ParseAPIKeysEnv` 丢弃空 token/name 条目 | 畸形环境变量注册空字符串 Key |
| **最小权限** | 每个 Key 独立配置 Scopes 列表 | 按服务/角色精确授权 |
| **通配符降级** | `"*"` Scope 授予所有权限 | 管理员/网关全量访问场景 |
| **身份隔离** | InternalKeys 和 ExternalKeys 分别存储、分别查找 | 内部服务与外部客户端权限域隔离 |
| **Fail-open 透明** | 认证未启用时注入 `AnonymousIdentity{Scopes:["*"]}` | 开发环境零配置可用 |
| **Fail-closed 审计** | 认证启用后，无效 Token 一律 401 | 生产环境裸奔检测 |

##### engine-go vs service-hub 鉴权差异对比

| 维度 | engine-go (`AuthMiddleware`) | service-hub (`scopeAuthMiddleware`) |
|---|---|---|
| **路径映射函数** | `PermissionForRESTPath()` | `ServiceHubPermissionForPath()` |
| **Key 存储** | `InternalKeys` + `ExternalKeys` 双池 | `ScopeKeys` 单池 |
| **权限空间** | `privacy:*`, `classification:*`, `ops:*` 等 | `hub:read`, `hub:dispatch` |
| **回退模式** | 无（仅 Scope-based） | 有（`SERVICE_HUB_API_KEY` 单密钥回退） |
| **健康端点** | `IsHealthPathOrMethod` + `HealthNoAuth` 配置 | 路径映射返回 `""` 直接放行 |
| **gRPC 鉴权** | mTLS CN 白名单拦截器 | 无 gRPC 端口 |
| **匿名模式** | `AuthEnabled=false` → `AnonymousIdentity` | 无匿名模式（必须认证） |

#### 2.2.3.2 service-hub 对外接口 Scope-based 权限控制

**背景**：service-hub 是整个 PrivShield 微服务群中**唯一对外网提供服务**的组件（其他服务如 datasource-mgr、audit-log 均部署在政务云内网 VPC），其鉴权强度直接关系到整个政务云数据流通链路的安全性。

**双模式鉴权中间件**（`scopeAuthMiddleware`）：

service-hub 实现了向后兼容的双模式鉴权，优先使用 Scope-based 细粒度权限，回退到单 API Key 简单模式：

```go
// services/service-hub/internal/handlers/handlers.go
func (s *Server) scopeAuthMiddleware() gin.HandlerFunc {
    // 模式 1：Scope-based 模式（SERVICE_HUB_API_KEYS 已配置）
    if len(s.cfg.ScopeKeys) > 0 {
        return func(c *gin.Context) {
            token := extractBearerToken(c)
            if token == "" {
                AbortWithError(c, 401, "UNAUTHENTICATED", ...)
                return
            }
            identity := constantTimeLookupKeys(s.cfg.ScopeKeys, token)
            if identity == nil {
                AbortWithError(c, 401, "UNAUTHENTICATED", ...)
                return
            }
            // 路径 → 所需权限映射
            requiredPerm := pkgauth.ServiceHubPermissionForPath(c.Request.URL.Path)
            if requiredPerm != "" && !identity.HasPermission(requiredPerm) {
                AbortWithError(c, 403, "FORBIDDEN", ...)
                return
            }
            c.Set("identity", identity)
            c.Next()
        }
    }
    // 模式 2：单 API Key 兼容模式（SERVICE_HUB_API_KEY）
    return middleware.Auth(s.cfg.APIKey)
}
```

**service-hub 路径 → 权限映射**（`ServiceHubPermissionForPath`）：

| 路径 | 权限字符串 | 说明 |
|---|---|---|
| `/health`, `/readyz`, `/api/health` | `""` (开放) | 健康探针，无需特定权限 |
| `/metrics` | `""` (开放) | Prometheus 指标导出 |
| `/api/hub/status` | `hub:read` | 调度中枢状态查询 |
| `/api/hub/tasks`, `/api/hub/tasks/:id` | `hub:read` | 任务列表/详情查询 |
| `/api/hub/pipeline` | `hub:read` | 流水线监控遥测 |
| `/api/hub/dispatch` | `hub:dispatch` | 核心：分发隐私处理任务 |
| `/api/hub/classify` | `hub:dispatch` | 分类+分发 |

**设计要点**：
- `hub:dispatch` 权限仅授予需要触发数据流通流水线的调用方（如 BFF 网关），只读查询用 `hub:read`
- 尾部斜杠归一化防止 `/api/hub/dispatch/` 绕过权限映射
- `constantTimeLookupKeys` 与 `pkg/auth` 的 `ConstantTimeLookup` 原理一致：排序 key + 全量遍历 + `subtle.ConstantTimeCompare`

**API Key 环境变量格式**：

```bash
# SERVICE_HUB_API_KEYS 格式：token:name:scope1,scope2;token2:name2:scope3
SERVICE_HUB_API_KEYS="sk-hub-abc:service-hub:hub:read,hub:dispatch;sk-readonly:monitor:hub:read"

# 向后兼容：SERVICE_HUB_API_KEY 单密钥模式（所有已认证请求获得同等权限）
SERVICE_HUB_API_KEY="sk-simple-key"
```

**`ParseAPIKeysEnv` 共享解析器**（`pkg/auth/identity.go`）：

```go
// pkg/auth/identity.go L175-209
func ParseAPIKeysEnv(raw string) map[string]*KeyConfig {
    // 按 ";" 分割多 Key → 按 ":" 分割 token:name:scopes → 按 "," 分割 scopes
    // 安全增强：TrimSpace 防前后空格、丢弃空 token 防注册空字符串 Key
    // 缺少 scopes 时默认 ["*"]（全部权限）
}
```

该解析器被 engine-go（`PRIVACY_AUTH_INTERNAL_API_KEYS`）和 service-hub（`SERVICE_HUB_API_KEYS`）共享，确保全项目密钥解析逻辑一致。

**环境变量速查**：

| 环境变量 | 所属服务 | 说明 |
|---|---|---|
| `SERVICE_HUB_API_KEYS` | service-hub | Scope-based 多密钥配置（推荐） |
| `SERVICE_HUB_API_KEY` | service-hub | 单密钥兼容模式（回退） |
| `PRIVACY_AUTH_INTERNAL_API_KEYS` | engine-go | 内部服务 Scope-based 多密钥 |
| `PRIVACY_AUTH_EXTERNAL_API_KEYS` | engine-go | 外部客户端 Scope-based 多密钥 |

**internal/external 身份分离的设计意图**：
- `internal` 身份：由内部服务（service-hub、bff-go）持有，通常授予更宽泛的 scope
- `external` 身份：由外部客户端持有，scope 受最小权限约束
- 认证时先查 internal keys，再查 external keys — internal 优先

#### 2.2.4 32 分片令牌桶限流

**源码位置**：`pkg/middleware/ratelimit.go` (L80-224)

**DDoS 纵深防御三层架构**：
```
┌─────────────────────────────────────────────────────────┐
│ 第 1 层：MaxBodySize (32 MiB)                           │
│   → 防御大包 OOM 攻击，超出返回 413                      │
├─────────────────────────────────────────────────────────┤
│ 第 2 层：MaxConcurrent (1000 并发)                       │
│   → 防御瞬时峰值耗尽资源，超出返回 503                    │
├─────────────────────────────────────────────────────────┤
│ 第 3 层：RateLimit (32 分片令牌桶)                       │
│   → 平滑限流 HTTP Flood，超出返回 429 + Retry-After      │
└─────────────────────────────────────────────────────────┘
```

**架构设计**：
```
shardedRateLimiter 结构：
├── shards[32] *rateLimitShard     // 32 个独立分片
│   ├── mu: sync.Mutex             // 分片级互斥锁
│   └── buckets: map[string]*bucket // key → 令牌桶
├── stopCh: chan struct{}           // 停机信号
└── 后台 GC 协程 (每 3 分钟清理 >10 分钟未活动的桶)
```

**为何分片**：
- 单锁限流器在高并发下成为瓶颈 — 所有请求竞争同一把锁
- 32 分片将锁竞争降低到 1/32（FNV-1a 哈希分片）
- 分片键选择：`Identity.ServiceType + ":" + Identity.Name + ":" + normalizedPath`
- 匿名调用者追加 `ClientIP` 作为分片因子，防止单 IP 洪泛攻击

**FNV-1a 分片哈希**：
```go
// pkg/middleware/ratelimit.go L124-131
func (l *shardedRateLimiter) shardFor(key string) *rateLimitShard {
    var h uint32 = 2166136261  // FNV offset basis
    for i := 0; i < len(key); i++ {
        h ^= uint32(key[i])
        h *= 16777619         // FNV prime
    }
    return l.shards[h%numRateLimitShards]
}
```

**令牌桶算法**（`allow()` L133-157）：
```go
// pkg/middleware/ratelimit.go L133-157
func (l *shardedRateLimiter) allow(key string, rps, burst float64) bool {
    shard := l.shardFor(key)
    shard.mu.Lock()
    defer shard.mu.Unlock()

    now := time.Now()
    b, ok := shard.buckets[key]
    if !ok {
        // 新 key：初始令牌 = burst（允许首次突发）
        b = &rateLimitBucket{tokens: burst, lastCheck: now}
        shard.buckets[key] = b
    }

    // 补充令牌：按恒定速率 rps 补充
    elapsed := now.Sub(b.lastCheck).Seconds()
    b.tokens += elapsed * rps
    if b.tokens > burst {
        b.tokens = burst  // 上限截断，防止令牌堆积
    }
    b.lastCheck = now

    if b.tokens < 1.0 {
        return false  // 令牌不足，拒绝
    }
    b.tokens -= 1.0   // 消耗 1 令牌
    return true
}
```

**路径归一化**（`NormalizeRateLimitPath` L271-282）：
- 将路径中的动态 ID 段（纯数字、UUID 格式）替换为 `:id` 占位符
- 防止高基数路径（如 `/api/users/12345`）导致桶爆炸

```go
// 示例：
// /api/users/12345      → /api/users/:id
// /api/orders/550e8400-... → /api/orders/:id
```

#### 2.2.5 安全头中间件

**源码位置**：`engine-go/internal/security/auth.go` (L33-39)

```go
func SecurityHeadersMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        pkgmiddleware.SecurityHeadersTo(c.Writer)  // CSP, HSTS, X-Content-Type-Options 等
        c.Writer.Header().Set("X-Frame-Options", "DENY")  // 禁止 iframe 嵌套
        c.Next()
    }
}
```

**安全头清单**：
| 头部 | 值 | 防御目标 |
|---|---|---|
| `X-Frame-Options` | `DENY` | 点击劫持（Clickjacking） |
| `X-Content-Type-Options` | `nosniff` | MIME 类型嗅探 |
| `X-XSS-Protection` | `1; mode=block` | 反射型 XSS |
| `Strict-Transport-Security` | `max-age=31536000` | SSL 降级攻击 |
| `Content-Security-Policy` | `default-src 'none'` | XSS / 数据注入 |

#### 2.2.6 gRPC mTLS CN 拦截器

**源码位置**：`pkg/tlsutil/grpc_interceptor.go` (L38-134)

**鉴权流程**：
1. `extractClientCN(ctx)` → 从 `peer.Peer.AuthInfo` 提取 `credentials.TLSInfo`
2. 深入 `VerifiedChains[0][0].Subject.CommonName` 获取经过 CA 验证的 CN
3. `authorizeClient(cn, fullMethod)` → 白名单存在性检查 + 方法级 Scope 匹配
4. 同时提供 `UnaryServerInterceptor` 和 `StreamServerInterceptor` — 一元与流式全覆盖

**快速装配工厂**：
```go
unaryInt, streamInt, whitelist, err := tlsutil.NewWhitelistInterceptor(path)
// path 为空 → 返回全 nil（禁用 CN 白名单鉴权）
// path 非空 → 自动加载 YAML + 启动热重载 + 返回拦截器
```

### 2.3 Day 3-4 Review 方法

1. **对照配置加载**：从 `config.go` 的 `loadSettings()` 开始，追踪每个环境变量的加载路径
2. **追踪认证链路**：HTTP 请求 → `AuthMiddleware` → `ExtractBearerToken` → `ConstantTimeLookup` → `authenticateAPIKey` → `PermissionForRESTPath` → `HasPermission`
3. **对比两套白名单**：engine-go `WhitelistManager` vs pkg `DynamicWhitelist`，理解设计差异与适用场景
4. **安全审计清单**：
   - [x] ~~REST 路径到权限的映射是否有遗漏~~ → **已修复**：路径归一化 `/api/v1/*` → `/v1/*` + 根路径别名 + 快捷别名路由全覆盖（50+ 测试用例）。含 SEC-12（`dynclassification:write` 读写区分）和 SEC-13（`budget/reset` 映射补充）两个子项
   - [x] ~~service-hub 对外接口是否具备独立 Scope 鉴权~~ → **已修复**：`scopeAuthMiddleware` 双模式鉴权 + `ServiceHubPermissionForPath` 路径映射
   - [x] ~~API Key 解析逻辑是否在各服务间一致~~ → **已修复**：`ParseAPIKeysEnv` 共享解析器下沉到 `pkg/auth`，engine-go 与 service-hub 统一使用
   - [x] ~~mTLS CN 白名单是否已接入 Gin 主中间件链~~ → **已修复**：`pkg/tlsutil/grpc_interceptor.go` 一元 + 流式拦截器全覆盖
   - [x] ~~API Key 无过期机制~~ → **已修复**（G-14）：`KeyConfig.ExpiresAt` + `IsExpired()` + RFC3339 环境配置
   - [ ] 限流完全基于内存，多实例部署时无法共享限流状态
   - [ ] 新增端点时是否同步添加权限映射（需代码 Review 流程保障，防止 SEC-09/12/13 类问题复发）

### 2.4 安全体系纵深分析

#### 2.4.1 时序攻击与常量时间比较详解

**什么是时序攻击？**

时序攻击（Timing Attack）是一种侧信道攻击，攻击者通过精确测量响应时间来逐字符猜解密钥。

标准字符串比较的伪代码：
```go
// 标准 == 比较：发现第一个不同字符就返回
func equal(a, b string) bool {
    if len(a) != len(b) { return false }
    for i := 0; i < len(a); i++ {
        if a[i] != b[i] { return false }  // 提前返回
    }
    return true
}
```

攻击过程：
```
假设密钥为 "secret"（6字符）

尝试 "a_____" → 响应时间 100ns（第1个字符就不同）
尝试 "s_____" → 响应时间 150ns（第1个字符匹配，第2个不同）
尝试 "se____" → 响应时间 200ns（前2个字符匹配）
...
尝试 "secret" → 响应时间 350ns（全部匹配）→ 破解成功！
```

**`subtle.ConstantTimeCompare` 如何防御**：
```go
// crypto/subtle.ConstantTimeCompare 的简化实现
func ConstantTimeCompare(x, y []byte) int {
    if len(x) != len(y) { return 0 }
    var v byte
    for i := 0; i < len(x); i++ {
        v |= x[i] ^ y[i]  // 异或累积，不提前返回
    }
    // v == 0 当且仅当所有字节都相同
    return int((v - 1) >> 8) + 1  // 恒定时间转换为 0 或 1
}
```

关键：无论在第几个字符不同，执行时间始终相同。

**本项目的额外防护：排序 + 全量遍历**

```go
// pkg/auth/middleware.go L55-75
func ConstantTimeLookup(keys map[string]*KeyConfig, token string) *KeyConfig {
    // 1. 排序 key：消除 Go map 迭代随机性带来的时序差异
    sortedKeys := make([]string, 0, len(keys))
    for k := range keys { sortedKeys = append(sortedKeys, k) }
    sort.Strings(sortedKeys)

    // 2. 遍历全部 key，不提前 break
    var matched *KeyConfig
    for _, key := range sortedKeys {
        if subtle.ConstantTimeCompare([]byte(key), tokenBytes) == 1 {
            matched = keys[key]  // 记录但不 break
        }
    }
    return matched
}
```

为何需要排序？Go map 的迭代顺序是随机的，如果不排序，每次请求遍历 key 的顺序可能不同，导致响应时间有微小差异。排序确保每次迭代的顺序完全一致。

#### 2.4.2 权限模型设计哲学

**为何选择 Scope-based 而非 RBAC？**

| 维度 | Scope-based (本项目) | RBAC (角色-based) |
|---|---|---|
| 粒度 | 接口级权限 | 角色级权限 |
| 配置复杂度 | 低（每个 Key 直接配 scope 列表） | 高（需维护角色→权限映射） |
| 适用场景 | 微服务间调用 | 企业用户权限管理 |
| 灵活性 | 高（可按需组合 scope） | 中（受角色定义约束） |

本项目选择 Scope-based 的原因：
1. 主要是微服务间调用，不需要复杂的角色层级
2. 每个 API Key 的权限需求明确且固定
3. 减少配置层次，降低出错概率

**权限映射的路径归一化与 fail-closed 设计**：

```go
// pkg/auth/identity.go L50-61
func PermissionForRESTPath(path string) string {
    // 去除尾部斜杠
    if len(path) > 1 && path[len(path)-1] == '/' {
        path = path[:len(path)-1]
    }
    // 归一化：/api/v1/* → /v1/*，确保别名路由与主路由共享同一权限映射。
    normalized := path
    if strings.HasPrefix(normalized, "/api/v1/") {
        normalized = "/v1/" + normalized[len("/api/v1/"):]
    } else if normalized == "/api/v1" {
        normalized = "/v1"
    }
    switch {
    case strings.HasPrefix(normalized, "/v1/privacy/mask"):
        return "privacy:mask"
    // ... 其他已知路径
    }
    return ""  // 未知路径返回空串，表示无需特定权限
}
```

> **已修复的安全漏洞**：早期版本只匹配 `/v1/*` 前缀，但 Gin 路由同时注册了 `/api/v1/*` 别名路由和根路径（`/agent/process` 等），导致 40+ 路由完全绕过权限校验。修复方案是在函数入口统一归一化路径前缀，而非为每条别名路由重复 case 分支。

注意：未映射的路径返回空串，表示对所有已认证身份开放。这是一个设计决策：
- 优点：新端点不会因为忘记配权限映射而导致 403
- 风险：如果某个端点需要权限保护但忘记添加映射，会被意外开放
- 缓解：通过代码 Review 和测试用例（50+ 路径覆盖）确保所有敏感端点都有映射

#### 2.4.3 32 分片限流器性能分析

**分片策略的数学分析**：

假设并发请求数为 N，分片数为 S：
- 单锁限流器：所有 N 个请求竞争 1 把锁，锁等待时间 ∝ N
- 32 分片限流器：N 个请求分散到 32 个分片，每个分片平均 N/32 个请求
- 锁竞争降低到约 1/32

**FNV-1a 哈希的选择理由**：
```go
var h uint32 = 2166136261  // FNV offset basis
for i := 0; i < len(key); i++ {
    h ^= uint32(key[i])
    h *= 16777619         // FNV prime
}
```

- FNV-1a 是已知的分布最均匀的简单哈希之一
- 计算速度极快（仅异或 + 乘法）
- 对于短字符串（如 IP 地址、路径）的分布特别好
- 无需引入外部哈希库

**令牌桶的 GC 策略**：

```go
// 后台协程每 3 分钟清理超过 10 分钟未活动的桶
func (l *shardedRateLimiter) cleanup(ttl time.Duration) {
    now := time.Now()
    for i := 0; i < numRateLimitShards; i++ {
        shard := l.shards[i]
        shard.mu.Lock()
        for k, b := range shard.buckets {
            if now.Sub(b.lastCheck) > ttl {
                delete(shard.buckets, k)
            }
        }
        shard.mu.Unlock()
    }
}
```

设计决策：
- TTL = 10 分钟：足够长，避免误删活跃用户的桶；足够短，避免内存持续增长
- 清理间隔 = 3 分钟：平衡清理频率和 CPU 开销
- 逐分片加锁：清理某个分片时不影响其他分片的限流服务

#### 2.4.4 mTLS 白名单热重载对比分析

**两套实现的设计差异根源**：

```
engine-go/internal/security/whitelist.go
├── 设计目标：Agent 专用，REST 侧
├── 重载策略：请求驱动（被动）
│   └── 每次 GetEntry() 时检查 mtime
├── 优势：无后台协程，资源占用为零
└── 劣势：无请求时不会重载

pkg/tlsutil/whitelist.go
├── 设计目标：共享基础库，gRPC 侧
├── 重载策略：后台轮询（主动）
│   └── 独立 goroutine 每 5s 检查 mtime
├── 优势：及时重载，无请求延迟
└── 劣势：常驻后台协程，需优雅停机
```

**为何存在两套实现？**

历史原因：engine-go 先实现了 Agent 专用的白名单管理器，后来在构建 services 和 console 时，需要一个更通用的版本（支持后台轮询、Scope 通配符、优雅停机），因此在 pkg/tlsutil 中重新实现。

**收敛建议**：

长期应将两套实现收敛为 pkg/tlsutil 中的统一版本：
1. pkg 版本功能更完整（支持通配符、优雅停机）
2. 请求驱动重载可以通过在 `CheckScope` 中增加 mtime 检查来实现
3. 消除两套实现的语义漂移风险

#### 2.4.5 DDoS 纵深防御策略详解

```
攻击类型                    防御层                    响应
────────────────────────────────────────────────────────────
大包 OOM 攻击           → MaxBodySize (32MiB)    → 413 Payload Too Large
瞬时并发洪峰            → MaxConcurrent (1000)   → 503 UPSTREAM_UNAVAILABLE
持续 HTTP Flood         → RateLimit (令牌桶)      → 429 RATE_LIMITED + Retry-After
慢速攻击 (Slowloris)    → http.Server.ReadTimeout → 连接超时断开
协议层攻击              → TLS 1.3 最低版本         → 拒绝不安全连接
```

**MaxConcurrent 的 Channel 信号量模式**：

```go
// pkg/middleware/ratelimit.go L56-75
func MaxConcurrent(limit int) gin.HandlerFunc {
    sem := make(chan struct{}, limit)  // 容量为 limit 的信号量

    return func(c *gin.Context) {
        select {
        case sem <- struct{}{}:        // 尝试获取令牌
            defer func() { <-sem }()   // 请求结束后释放
            c.Next()
        default:                        // 队列已满，非阻塞拒绝
            AbortWithError(c, http.StatusServiceUnavailable, ...)
        }
    }
}
```

为何用 Channel 而非 `sync.Mutex` + 计数器？
- Channel 天然支持阻塞/非阻塞语义（`select` + `default`）
- 无需手动管理计数器的增减
- Go runtime 对 Channel 有高度优化

---

### 2.5 接口权限控制漏洞修复案例（SEC-09 ~ SEC-13）

以下漏洞在安全 Review 中发现并已修复，作为权限控制学习的实战案例。

#### SEC-09：`PermissionForRESTPath` 别名路由权限绕过（Critical）

**漏洞描述**：`PermissionForRESTPath()` 仅匹配 `/v1/*` 前缀，但 Gin 路由同时注册了 `/api/v1/*` 别名路由（如 `/api/v1/privacy/mask`）和根路径别名（如 `/agent/process`），导致 40+ 路由完全绕过 Scope 权限校验。任何持有有效 API Key 的调用方可访问任意未映射路径。

**影响范围**：engine-go REST API 全部别名路由

**修复方案**：在 `PermissionForRESTPath()` 入口统一执行路径归一化 `/api/v1/*` → `/v1/*`，并补充根路径别名和快捷别名的 case 分支。

**修复文件**：`pkg/auth/identity.go`

**测试覆盖**：`pkg/auth/identity_test.go` 新增 50+ 路径映射测试用例

#### SEC-10：service-hub 缺少 Scope-based 细粒度鉴权（High）

**漏洞描述**：service-hub 作为唯一对外网提供服务的微服务，仅支持单 API Key 认证（`SERVICE_HUB_API_KEY`），所有已认证请求具有同等权限，无法区分只读查询（`hub:read`）和任务分发（`hub:dispatch`）。

**影响范围**：`services/service-hub/` 全部 API 端点

**修复方案**：
1. 新增 `SERVICE_HUB_API_KEYS` 环境变量支持 Scope-based 多密钥配置
2. 实现 `scopeAuthMiddleware()` 双模式鉴权中间件
3. 新增 `ServiceHubPermissionForPath()` 路径→权限映射
4. 共享 `ParseAPIKeysEnv()` 解析器到 `pkg/auth`

**修复文件**：`services/service-hub/internal/handlers/handlers.go`、`services/service-hub/internal/config/config.go`、`pkg/auth/identity.go`

#### SEC-11：engine-go `parseAPIKeys` 重复实现导致逻辑漂移（Medium）

**漏洞描述**：engine-go 的 `internal/security/config.go` 和 service-hub 各自实现了 `parseAPIKeys()` 函数，逻辑不完全一致（如空 token 处理、TrimSpace），存在安全语义漂移风险。

**修复方案**：将 `parseAPIKeys` 提取为 `pkg/auth.ParseAPIKeysEnv()`，增加安全增强（TrimSpace、丢弃空 token），engine-go 和 service-hub 统一调用共享实现。

**修复文件**：`pkg/auth/identity.go`（新增）、`engine-go/internal/security/config.go`（删除私有实现）

#### SEC-12：动态分类分级读写权限未区分（Medium）

**漏洞描述**：`/v1/dynclassification/profiles/reload`（写操作：重载分类 Profile）和 `/v1/dynclassification`（读操作：执行分类）共享同一权限映射，任何具有 `dynclassification:read` 权限的调用方可触发 Profile 重载。

**修复方案**：区分 `dynclassification:write`（reload/generate_profile）和 `dynclassification:read`（其他动态分类路径），写操作默认不映射到读权限。

#### SEC-13：`/v1/privacy/budget/reset` 权限映射缺失（Medium）

**漏洞描述**：隐私预算重置端点 `/v1/privacy/budget/reset` 未被 `PermissionForRESTPath()` 映射，任何已认证调用方可重置隐私预算，绕过预算耗尽限制。

**修复方案**：新增 `budget/reset` 映射到 `privacy:budget` 权限。

---

## 第 4 章：三级等保/密评合规增强

> 本章记录对标 GB/T 22239-2019（三级等保）与 GM/T 0115-2023（密评）标准完成的 7 项合规修复。详见 `docs/production_security/compliance_gap_analysis.md`。

### 4.1 G-03 登录失败处理与账号锁定

**对标**：三级等保 身份鉴别  
**文件**：`console/app-lz/bff-go/internal/auth/userstore.go`

| 参数 | 值 | 说明 |
|------|-----|------|
| `maxFailedAttempts` | 5 | 连续失败次数阈值 |
| `lockoutDuration` | 15 分钟 | 锁定时长 |
| 存储 | `failedAttempts map[string]int` + `lockedUntil map[string]time.Time` | 内存级，`sync.RWMutex` 保护 |

认证流程：`Authenticate()` 入口检查 `IsLocked()` → 已锁定返回 `ErrAccountLocked`（HTTP 423）→ 失败 `recordFailedLogin()` → 成功 `resetFailedLogin()`。

### 4.2 G-04 密码策略强化

**对标**：三级等保 身份鉴别  
**文件**：`console/app-lz/bff-go/internal/auth/userstore.go` `validatePasswordStrength()`

| 规则 | 要求 |
|------|------|
| 最低长度 | 12 字符 |
| 字符类混合 | 4 类（大写/小写/数字/特殊）中至少 3 类 |
| 弱密码字典 | 16 个常见弱密码黑名单 |
| 用户名包含 | 密码不得包含用户名（大小写不敏感） |

### 4.3 G-05 JWT 令牌吊销

**对标**：三级等保 访问控制  
**文件**：`console/app-lz/bff-go/internal/auth/jwt.go`

```go
type JWTManager struct {
    secret      []byte
    expiryHours int
    blacklist   sync.Map  // SHA-256(token) → expiryUnix
}
```

- `RevokeToken(token)`：SHA-256 哈希存入黑名单
- `ValidateToken(token)`：签名 → 过期 → 黑名单 三阶段校验
- `cleanupLoop()`：每 10 分钟清理过期条目
- `HandleLogout()`：`/logout` 端点调用 `RevokeToken()`

### 4.4 G-11 TOTP 双因素认证

**对标**：三级等保 身份鉴别  
**文件**：`console/app-lz/bff-go/internal/auth/totp.go`

| 参数 | 值 | 说明 |
|------|-----|------|
| 密钥长度 | 20 字节（160-bit） | RFC 推荐最低值 |
| 验证码 | 6 位数字 | 标准 TOTP 输出 |
| 时间步长 | 30 秒 | RFC 6238 |
| 容忍窗口 | ±1 步 | 容忍时钟偏差 |

核心：`GenerateSecret()` → `ValidateCode()` (RFC 6238 HMAC-SHA1 + 动态截断) → `GenerateOTPAuthURL()` (otpauth:// URI)。

### 4.5 G-12 WAF Web 攻击防护

**对标**：三级等保 入侵防范  
**文件**：`pkg/middleware/waf.go`

5 类 72 条正则规则（`init()` 编译，运行时不可变）：

| 类别 | 规则数 | 典型检测 |
|------|:------:|---------|
| SQL 注入 | 21 | UNION SELECT、OR 1=1、SLEEP 盲注 |
| XSS | 18 | `<script>`、`javascript:`、`on\w+=` |
| 命令注入 | 12 | 管道 + 系统命令、反引号替换 |
| 路径穿越 | 14 | `../`、URL 编码变体、`/etc/passwd` |
| 已知漏洞 | 7 | Log4Shell、Shellshock、Spring4Shell |

扫描范围：URL path + query + 5 种 header + body（≤1 MiB）。命中返回 `403 WAF_BLOCKED`。

### 4.6 G-14 API Key 过期与自动轮换

**对标**：三级等保 访问控制 + 密评 密钥管理  
**文件**：`pkg/auth/settings.go`、`pkg/auth/identity.go`、`pkg/auth/middleware.go`

- `KeyConfig.ExpiresAt *time.Time`（nil = 永不过期）
- `IsExpired()` → 认证时拒绝过期 Key
- 环境变量格式扩展：`token:name:scopes:2025-12-31T23:59:59Z`
- `findExpirySeparator()` 回溯扫描，兼容含冒号 scope（如 `privacy:mask`）

### 4.7 G-02 可信代理与源地址校验

**对标**：三级等保 入侵防范  
**文件**：`pkg/middleware/middleware.go`、`pkg/middleware/ratelimit.go`

- `ConfigureTrustedProxies(r, trustedProxies)` → 仅受信代理的 `X-Forwarded-For` 被采信
- `RealClientIP(c)` → `c.ClientIP()` + RemoteAddr 回退
- `PRIVACY_TRUSTED_PROXIES` 环境变量，未配置时不信任任何代理
- 限流 key、WAF 日志统一使用 `RealClientIP`

---

## 第 5 章：Day 5 可观测性 Review（日志、指标、追踪）

### 5.1 审查文件清单

| 文件路径 | 行数 | 核心职责 |
|---|:---:|---|
| `pkg/observability/metrics.go` | 125 | REDMetrics（Rate/Errors/Duration）Prometheus 指标 |
| `pkg/observability/tracing.go` | 94 | Tracer 抽象接口 + NoOpTracer + OTelTracer 占位 |
| `pkg/observability/logger.go` | 47 | 结构化 slog 日志初始化（JSON/Text） |
| `pkg/observability/request_logger.go` | 56 | HTTP 请求日志 Gin 中间件 |
| `pkg/observability/trace.go` | 126 | TraceID 生成 + 追踪上下文传播中间件 |
| `engine-go/internal/observability/metrics.go` | 140 | EngineMetrics（RED + 业务指标扩展） |
| `engine-go/internal/observability/gateway_metrics.go` | 137 | GatewayMetrics（InFlight/EWMA/熔断器/转发计数） |
| `pkg/middleware/trace.go` | 53 | 追踪中间件兼容别名（下沉至 pkg/observability） |

### 5.2 核心知识点详解

#### 5.2.1 Prometheus RED 指标体系

**源码位置**：`pkg/observability/metrics.go` (L22-125)

**RED 方法论**：
- **Rate**（请求率）：`privshield_requests_total` — 按 protocol/endpoint/status 分组的计数器
- **Errors**（错误率）：从 `RequestsTotal` 的 status 标签中提取 5xx 状态码计算
- **Duration**（延迟）：`privshield_request_duration_seconds` — 按 protocol/endpoint 分组的直方图

**完整数据结构与初始化**：
```go
// pkg/observability/metrics.go L22-57
type REDMetrics struct {
    registry *prometheus.Registry  // 独立注册表，多模块不冲突

    RequestsTotal   *prometheus.CounterVec    // 请求计数器
    RequestDuration *prometheus.HistogramVec  // 延迟直方图
}

func NewREDMetrics() *REDMetrics {
    reg := prometheus.NewRegistry()  // 独立注册表（非全局默认）

    m := &REDMetrics{
        registry: reg,
        RequestsTotal: prometheus.NewCounterVec(
            prometheus.CounterOpts{
                Name: "privshield_requests_total",
                Help: "Total requests processed.",
            },
            []string{"protocol", "endpoint", "status"},
        ),
        RequestDuration: prometheus.NewHistogramVec(
            prometheus.HistogramOpts{
                Name:    "privshield_request_duration_seconds",
                Help:    "Request latency histogram in seconds.",
                Buckets: prometheus.DefBuckets,  // 默认桶: .005, .01, .025, ... 10
            },
            []string{"protocol", "endpoint"},
        ),
    }
    reg.MustRegister(m.RequestsTotal, m.RequestDuration)
    return m
}
```

**设计要点**：
- 使用独立 `prometheus.Registry`（非全局默认注册表）— 多模块共存不冲突
- 提供 `GinHandler()` 和 `PrometheusMiddleware()` — 直接嵌入 Gin 路由
- 同时提供 HTTP 中间件和 gRPC `UnaryServerInterceptor` — 双协议自动埋点

**PrometheusMiddleware 自动埋点**：
```go
// pkg/observability/metrics.go L92-109
func (m *REDMetrics) PrometheusMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        c.Next()

        path := c.FullPath()
        if path == "" {
            path = c.Request.URL.Path
        }
        if path == "/metrics" {
            return  // /metrics 自身不记录，避免自抓取导致指标无限自增
        }

        duration := time.Since(start).Seconds()
        m.RecordRequest("http", path, c.Writer.Status(), duration)
    }
}
```

#### 3.2.2 EngineMetrics 业务指标扩展

**源码位置**：`engine-go/internal/observability/metrics.go` (L17-140)

```
EngineMetrics 继承 REDMetrics + 5 个业务指标：
├── ClassificationTotal      // 分类命中计数 (engine/level/domain)
├── BudgetConsumedTotal      // DP 预算消耗 (namespace/mechanism)
├── NerInferenceSeconds      // NER 推理耗时直方图 (device/batch_size)
├── APIAliasRequestsTotal    // 别名映射请求计数 (alias/canonical/target)
└── DatasourceNormalizeErrorsTotal // 标识归一化错误计数 (reason)
```

**指标命名规范**：统一 `privshield_` 前缀，标签使用有界枚举值（防止高基数爆炸）

#### 3.2.3 GatewayMetrics 网关指标

**源码位置**：`engine-go/internal/observability/gateway_metrics.go` (L19-137)

对齐设计文档 §11.1 网关指标规约：
| 指标名 | 类型 | 标签 | 含义 |
|---|---|---|---|
| `privshield_gateway_backend_in_flight` | Gauge | node_id, backend_addr | 实时在途请求数 |
| `privshield_gateway_backend_ewma_latency_seconds` | Gauge | node_id | EWMA 延迟 |
| `privshield_gateway_circuit_breaker_state` | Gauge | node_id, state | 熔断器状态 (0/1/2) |
| `privshield_gateway_requests_total` | Counter | node_id, status | 转发请求总数 |

#### 3.2.4 OpenTelemetry 追踪集成

**源码位置**：`pkg/observability/tracing.go` (L12-94)

**接口设计**：
```go
// pkg/observability/tracing.go L12-16
type Tracer interface {
    StartSpan(ctx context.Context, name string, attrs map[string]string) (context.Context, func())
}
```

**三种实现**：
| 实现 | 场景 | 行为 |
|---|---|---|
| `NoOpTracer` | 默认/未配置 OTel | 空操作，零开销 |
| `OTelTracer` | 配置了 `OTEL_EXPORTER_OTLP_ENDPOINT` | 占位实现（TODO: 接入 OTel SDK） |
| 全局单例 | `InitTracing()` 初始化 | `atomic.Pointer[Tracer]` + `sync.Once` |

**全局初始化逻辑**：
```go
// pkg/observability/tracing.go L48-72
func InitTracing(endpoint, serviceName string) Tracer {
    tracerOnce.Do(func() {
        if endpoint == "" {
            endpoint = os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
        }
        if serviceName == "" {
            serviceName = os.Getenv("PRIVACY_SERVICE_NAME")
            if serviceName == "" {
                serviceName = "PrivShield"
            }
        }

        var t Tracer
        if endpoint != "" {
            t = &OTelTracer{Endpoint: endpoint, ServiceName: serviceName}
        } else {
            t = &NoOpTracer{}  // 无端点 → 零开销 NoOp
        }
        tracer.Store(&t)
    })
    return *tracer.Load()
}
```

**当前状态**：`OTelTracer.StartSpan()` 实际退化为 no-op（L32-35），需要引入 `go.opentelemetry.io/otel` SDK 实现真实 Span 创建。

#### 3.2.5 结构化日志

**源码位置**：`pkg/observability/logger.go` (L17-47) + `request_logger.go` (L16-56)

**日志初始化**：
```go
// pkg/observability/logger.go L17-40
func NewLogger(format, level string) *slog.Logger {
    var logLevel slog.Level
    switch strings.ToLower(level) {
    case "debug":
        logLevel = slog.LevelDebug
    case "warn", "warning":
        logLevel = slog.LevelWarn
    case "error":
        logLevel = slog.LevelError
    default:
        logLevel = slog.LevelInfo
    }

    opts := &slog.HandlerOptions{Level: logLevel}
    var handler slog.Handler
    if strings.ToLower(format) == "text" {
        handler = slog.NewTextHandler(os.Stdout, opts)
    } else {
        handler = slog.NewJSONHandler(os.Stdout, opts)  // 生产推荐 JSON
    }
    return slog.New(handler)
}
```

- 基于 Go 1.21+ 标准库 `log/slog` — 零外部依赖
- 支持 JSON（默认，生产推荐）和 Text（开发调试）两种格式
- 支持 debug/info/warn/error 四级日志级别

**HTTP 请求日志字段**（`RequestLoggerWithModule`）：
```go
// pkg/observability/request_logger.go L28-56
func RequestLoggerWithModule(module string) gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        path := c.Request.URL.Path
        if c.Request.URL.RawQuery != "" {
            path = path + "?" + c.Request.URL.RawQuery  // 包含 query string
        }

        c.Next()

        latency := time.Since(start)
        status := c.Writer.Status()
        requestID := GetTraceID(c)

        args := []any{
            "request_id", requestID,
            "method", c.Request.Method,
            "path", path,
            "status", status,
            "latency_ms", latency.Milliseconds(),
            "client_ip", c.ClientIP(),
        }
        if module != "" {
            args = append(args, "module", module)
        }
        slog.Info("request completed", args...)
    }
}
```

输出示例：
```json
{"time":"2026-09-01T10:30:00Z","level":"INFO","msg":"request completed",
 "request_id":"req-20260901103000-abcdef01","method":"POST",
 "path":"/v1/privacy/mask","status":200,"latency_ms":12,"client_ip":"10.0.0.1"}
```

#### 3.2.6 分布式追踪上下文传播

**源码位置**：`pkg/observability/trace.go` (L50-126)

**TraceID 生成**（`GenerateRequestID`）：
```go
// pkg/observability/trace.go L52-59
func GenerateRequestID() string {
    var buf [4]byte
    _, _ = rand.Read(buf[:])  // 4 字节加密级安全随机数
    return "req-" + strings.Replace(
        time.Unix(0, time.Now().UnixNano()).Format("20060102150405.000000000"),
        ".", "-", 1,
    ) + "-" + hex.EncodeToString(buf[:])
}
```

- 格式：`req-<YYYYMMDDHHMMSS>-<纳秒>-<8位十六进制随机数>`
- 随机数来源：`crypto/rand.Read`（4 字节加密级安全随机）
- 保证纳秒级时间精度 + 加密随机后缀，碰撞概率极低

**TraceMiddleware 执行流程**：
```go
// pkg/observability/trace.go L96-125
func TraceMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        var traceID string

        // 1. 优先复用上游 RequestID() 中间件已生成的 request_id
        if rid, exists := c.Get("request_id"); exists {
            if s, ok := rid.(string); ok && s != "" {
                traceID = s
            }
        }

        // 2. 其次读取入站 X-Request-ID 请求头
        if traceID == "" {
            traceID = c.GetHeader(TraceHeader)
        }

        // 3. 若均为空，自动生成唯一 TraceID
        if traceID == "" {
            traceID = GenerateRequestID()
        }

        // 4. 双键名存储（兼容 + 专属）
        c.Set("request_id", traceID)
        c.Set(TraceIDContextKey, traceID)

        // 5. 写入双响应头
        c.Header(TraceHeader, traceID)
        c.Header(TraceIDHeader, traceID)

        // 6. 注入 request.Context() — 下游自动透传
        ctx := ContextWithRequestID(c.Request.Context(), traceID)
        c.Request = c.Request.WithContext(ctx)

        c.Next()
    }
}
```

**GetTraceID 4 级防空兜底**：
1. `TraceIDContextKey`（追踪专属键）
2. `"request_id"` 键（向后兼容）
3. 入站 `X-Request-ID` 请求头
4. 即时生成新 ID

### 5.3 Day 5 Review 方法

1. **指标暴露验证**：启动 engine-go，`curl localhost:8079/metrics`，确认所有指标名称和标签正确
2. **追踪链路验证**：配置 `OTEL_EXPORTER_OTLP_ENDPOINT`，发送请求后检查 Jaeger 是否看到 Span
3. **日志格式验证**：确认 JSON 格式日志包含所有必需字段（request_id, method, path, status, latency_ms）

### 5.4 Day 5 关注问题

- 追踪模块当前为薄封装，`OTelTracer.StartSpan()` 实际是 no-op — 引擎内部（分类漏斗各阶段）未创建独立 Span
- 审计关键事件（分类命中 LLM、熔断器状态变更、mTLS 证书即将过期）是否输出标准化日志
- 缺少 Prometheus 告警规则定义（`deploy/prometheus/rules/`）— 需要补充 P99 延迟、错误率、熔断器状态等告警

### 5.5 可观测性深度分析

#### 5.5.1 独立 Registry 的设计动机

```
Prometheus 默认注册表 (prometheus.DefaultRegisterer)
├── 全局单例，所有模块共享
├── 冲突风险：不同模块注册同名指标会 panic
└── 不适合多模块共存场景

本项目方案：独立 Registry
├── REDMetrics → 自己的 Registry
├── EngineMetrics → 继承 REDMetrics 的 Registry
├── GatewayMetrics → 继承 EngineMetrics 的 Registry
└── 优势：模块独立测试、互不干扰、可按需组合
```

```go
// pkg/observability/metrics.go L33-57
func NewREDMetrics() *REDMetrics {
    reg := prometheus.NewRegistry()  // 独立注册表
    // ...
    reg.MustRegister(m.RequestsTotal, m.RequestDuration)
    return m
}
```

这种设计允许：
1. 单元测试中创建独立的 REDMetrics 实例，不会与其他测试冲突
2. 不同服务可以按需组合指标（如 Gateway 不需要 Engine 的业务指标）
3. `/metrics` 端点只暴露本服务的指标

#### 5.5.2 TraceID 生成算法分析

```go
// pkg/observability/trace.go L52-59
func GenerateRequestID() string {
    var buf [4]byte
    _, _ = rand.Read(buf[:])  // crypto/rand，非 math/rand
    return "req-" + strings.Replace(
        time.Unix(0, time.Now().UnixNano()).Format("20060102150405.000000000"),
        ".", "-", 1,
    ) + "-" + hex.EncodeToString(buf[:])
}
```

**格式解析**：`req-20260901103000-123456789-a1b2c3d4`

| 部分 | 长度 | 来源 | 作用 |
|---|---|---|---|
| `req-` | 4 | 固定前缀 | 标识这是请求 ID |
| `20260901103000` | 14 | 时间戳（秒级） | 可读时间、排序 |
| `-123456789` | 9 | 纳秒精度 | 纳秒级区分 |
| `-a1b2c3d4` | 9 | crypto/rand 4字节 | 加密级随机性，防碰撞 |

**碰撞概率分析**：
- 纳秒时间戳 + 4 字节随机 = 2^32 ≈ 42 亿种组合/纳秒
- 即使在同一纳秒内发出 1000 个请求，碰撞概率也极低
- 使用 `crypto/rand` 而非 `math/rand`，确保随机性不可预测

#### 5.5.3 结构化日志最佳实践

**JSON 格式日志的优势**：
- 机器可解析：直接接入 ELK/Loki 等日志系统
- 字段类型保留：数字不会被转为字符串
- 嵌套支持：可以包含复杂结构

**本项目日志字段规范**：

| 字段 | 类型 | 必填 | 说明 |
|---|---|---|---|
| `request_id` | string | 是 | 追踪 ID，串联同一请求的所有日志 |
| `method` | string | 是 | HTTP 方法 (GET/POST/...) |
| `path` | string | 是 | 请求路径 + Query String |
| `status` | int | 是 | HTTP 状态码 |
| `latency_ms` | int64 | 是 | 延迟毫秒数 |
| `client_ip` | string | 是 | 客户端 IP |
| `module` | string | 否 | 模块标签（如 "gateway", "auth"） |

**日志级别使用规范**：

| 级别 | 使用场景 | 示例 |
|---|---|---|
| DEBUG | 开发调试信息 | 请求体内容、中间计算结果 |
| INFO | 正常业务流程 | 请求完成、服务启动 |
| WARN | 可恢复的异常 | 后端超时重试、证书即将过期 |
| ERROR | 不可恢复的错误 | 数据库连接失败、配置加载失败 |

#### 5.5.4 PrometheusMiddleware 的 /metrics 自引用问题

```go
// pkg/observability/metrics.go L92-109
func (m *REDMetrics) PrometheusMiddleware() gin.HandlerFunc {
    return func(c *gin.Context) {
        start := time.Now()
        c.Next()

        path := c.FullPath()
        if path == "/metrics" {
            return  // ← 关键：豁免 /metrics 自身
        }

        duration := time.Since(start).Seconds()
        m.RecordRequest("http", path, c.Writer.Status(), duration)
    }
}
```

**为何必须豁免 /metrics？**

如果不豁免，每次 Prometheus 抓取 `/metrics` 时都会产生一条新的指标记录，导致：
1. `privshield_requests_total{endpoint="/metrics"}` 无限自增
2. 抓取频率越高，自增越快
3. 污染真实的业务指标

这是一个常见的 Prometheus 集成陷阱。

---

## 第 6 章：代码走读指南

### 推荐读码顺序

按照以下顺序阅读代码，可以建立从浅到深的理解：

```
第 1 步：数据结构（理解”是什么”）
├── pkg/circuitbreaker/circuitbreaker.go     → 熔断器状态机
├── pkg/gateway/balancer.go (L26-87)         → BackendNode + BufferPool
├── pkg/auth/identity.go (L9-30)             → Identity + HasPermission
├── pkg/auth/identity.go (L50-130)           → PermissionForRESTPath 路径归一化与映射
└── pkg/auth/identity.go (L216-236)          → ServiceHubPermissionForPath

第 2 步：核心算法（理解”怎么做”）
├── pkg/gateway/balancer.go (L186-222)       → P2C-EWMA 选择算法
├── pkg/gateway/balancer.go (L265-289)       → Nginx SWRR 算法
├── pkg/auth/middleware.go (L55-75)          → 常量时间查找
└── pkg/auth/identity.go (L175-209)          → ParseAPIKeysEnv 共享解析器

第 3 步：集成层（理解”如何连接”）
├── engine-go/internal/gateway/http_proxy.go → HTTP 代理完整流程
├── engine-go/internal/gateway/grpc_proxy.go → gRPC 代理双向流
├── pkg/middleware/ratelimit.go              → 32 分片限流完整实现
└── services/service-hub/internal/handlers/handlers.go → scopeAuthMiddleware 双模式鉴权

第 4 步：可观测性（理解“如何监控”）
├── pkg/observability/metrics.go             → RED 指标定义与埋点
├── pkg/observability/trace.go               → TraceID 生成与传播
└── pkg/observability/logger.go              → 结构化日志初始化
```

### 关键代码路径追踪练习

**练习 1：追踪一个 REST 请求的完整生命周期**

从 `engine-go/cmd/privshield-gateway/main.go` 开始，追踪一个 POST `/v1/privacy/mask` 请求经过的所有中间件和处理器，画出完整的调用栈。

**练习 2：追踪熔断器状态转换**

模拟以下场景，画出熔断器状态转换图：
1. 后端正常响应 10 次
2. 后端连续返回 500 错误 5 次
3. 等待 30 秒冷却期
4. 后端恢复，探测请求成功 3 次

**练习 3：分析限流器的分片分布**

假设有 3 个客户端 A/B/C，分片数为 32，计算它们的限流 key 分别落在哪个分片：
- A: `internal:service-hub:/v1/privacy/mask`
- B: `external:client-1:/v1/privacy/dp/count`
- C: `anonymous:10.0.0.1:/v1/privacy/mask`

**练习 4：追踪别名路由的权限校验链路**

分别追踪以下 3 个请求的权限校验过程，说明路径归一化如何确保它们映射到相同的权限字符串：
1. `POST /api/v1/privacy/mask` → 归一化为 `/v1/privacy/mask` → `privacy:mask`
2. `POST /api/v1/mask` → 归一化为 `/v1/mask` → `privacy:mask`
3. `POST /v1/privacy/mask` → 无需归一化 → `privacy:mask`

再追踪一个 service-hub 请求：
4. `POST /api/hub/dispatch` → `ServiceHubPermissionForPath` → `hub:dispatch`，持有 `hub:read` scope 的调用方应被拒绝（403）

---

## 第 7 章：常见问题与排查指南

### Q1: 网关返回 503 SERVICE_UNAVAILABLE

**可能原因**：
1. 所有后端节点熔断器开启
2. 后端进程未启动或端口不可达

**排查步骤**：
```bash
# 1. 检查健康端点
curl http://localhost:8000/health

# 2. 检查后端状态
curl http://localhost:8000/health/backends

# 3. 检查后端进程是否存活
curl http://localhost:8079/health
```

### Q2: 熔断器始终处于 Open 状态

**可能原因**：
- 后端持续返回 5xx 错误
- 冷却时间设置过长

**排查步骤**：
```bash
# 查看熔断器状态
curl http://localhost:8000/health/backends | jq '.backends[].cb_state'
```

### Q3: 限流器误杀正常请求

**可能原因**：
- RPS 设置过低
- 路径归一化未覆盖动态 ID 格式

**排查步骤**：
```bash
# 检查限流响应头
curl -v http://localhost:8079/v1/privacy/mask 2>&1 | grep -i ratelimit
# 应看到 X-RateLimit-Limit 和 Retry-After 头
```

### Q4: Prometheus 指标不更新

**可能原因**：
- metrics 参数为 nil
- Prometheus 未配置抓取目标

**排查步骤**：
```bash
# 检查指标端点
curl http://localhost:8079/metrics | grep privshield_

# 检查 Prometheus 配置
cat deploy/prometheus/prometheus.yml
```

### Q5: 请求返回 403 FORBIDDEN

**可能原因**：
1. API Key 的 Scope 不包含该路径所需的权限字符串
2. 使用了别名路由（`/api/v1/*`）但权限映射未覆盖（已修复）
3. service-hub 持 `hub:read` scope 的 Key 调用了 `/api/hub/dispatch`（需要 `hub:dispatch`）

**排查步骤**：
```bash
# 1. 确认 Key 的 Scope 配置
echo $SERVICE_HUB_API_KEYS
# 格式：token:name:scope1,scope2

# 2. 确认路径映射的所需权限
# 查看 pkg/auth/identity.go PermissionForRESTPath()
# 或 services/service-hub/ ServiceHubPermissionForPath()

# 3. 测试：使用带 "*" scope 的 Key 排除 Scope 不足问题
curl -H "Authorization: Bearer <admin-key>" http://localhost:8082/api/hub/dispatch
```

---

## 第 8 章：术语表

| 术语 | 英文 | 说明 |
|---|---|---|
| P2C | Power of Two Choices | 随机选两个节点，选负载较轻的 |
| EWMA | Exponentially Weighted Moving Average | 指数移动加权平均 |
| SWRR | Smooth Weighted Round Robin | Nginx 平滑加权轮询 |
| CAS | Compare And Swap | 原子比较并交换操作 |
| mTLS | Mutual TLS | 双向 TLS 认证 |
| CN | Common Name | 证书主体通用名称 |
| RED | Rate/Errors/Duration | 微服务指标方法论 |
| InFlight | In-Flight Requests | 在途请求数 |
| BufferPool | Buffer Pool | 缓冲区复用池 |
| Fail-closed | Fail Closed | 故障时关闭（安全优先） |
| Sidecar | Sidecar Pattern | 边车模式 |
| 令牌桶 | Token Bucket | 平滑限流算法 |
| 时序攻击 | Timing Attack | 通过响应时间破解密钥 |
| 纵深防御 | Defense in Depth | 多层安全防御策略 |
| FNV-1a | Fowler-Noll-Vo hash | 快速非加密哈希函数 |
| RawCodec | Raw Codec | gRPC 原始编解码器（字节透传） |
| UnknownServiceHandler | Unknown Service Handler | gRPC 未注册方法拦截器 |
| NoOp | No Operation | 空操作占位实现 |
| Fail-safe | Fail Safe | 故障安全（优雅降级） |
| P0-1 | Priority 0-1 | 零信任默认态门禁 |
| Scope-based Auth | Scope-based Authorization | 基于权限字符串的接口级授权模型（非 RBAC），Identity 携带 Scopes 列表，权限校验通过精确匹配或通配符 `"*"` |
| 路径归一化 | Path Normalization | `/api/v1/*` → `/v1/*` 别名路由统一映射，防止别名路径绕过权限校验 |
| 别名路由 | Alias Route | 与主路由功能等价但路径前缀不同的注册路由（如 `/api/v1/mask` 是 `/v1/privacy/mask` 的别名） |
| 常量时间比较 | Constant-time Comparison | `subtle.ConstantTimeCompare` 防时序侧信道，排序 key + 全量遍历不提前 break |
| 双模式鉴权 | Dual-mode Authentication | Scope-based 优先 + 单 Key 兼容回退（`scopeAuthMiddleware`） |
| 共享解析器 | Shared Parser (`ParseAPIKeysEnv`) | `pkg/auth` 中统一的 API Key 解析函数，engine-go 与 service-hub 共享，含 TrimSpace + 空 token 丢弃 |
| 权限绕过 | Authorization Bypass | 因路径映射遗漏导致某些路由跳过 Scope 校验的安全漏洞（SEC-09 的核心问题） |

---

## 第 9章：Engine-go 启动流程与配置加载链路深度分析

理解进程启动流程是 Review 的基础——它揭示了各组件的初始化顺序、依赖关系和故障模式。

### Engine-go Agent 启动全流程

**源码位置**：`engine-go/cmd/privshield-agent/main.go` (324 行)

Agent 是 PrivShield 的核心隐私计算引擎，同时暴露 REST (:8079) 和 gRPC (:50051) 双协议端口。启动流程分为 7 个阶段：

```
阶段 1：配置加载
├── loadConfig() → 从环境变量加载 Runtime + 日志/限流配置
├── cfg.Validate() → P0-1 零信任门禁（fail-closed）
│   ├── 非环回监听必须配置入站凭据
│   ├── 声明 TLS 却未启用 → 终止进程
│   └── 启用 mTLS 却缺少白名单文件 → 终止进程
└── 门禁失败 → log.Fatalf() 立即终止（不打开任何端口）

阶段 2：日志与业务服务初始化
├── observability.InitLogger(level) → slog 结构化日志（JSON 格式）
├── service.NewPrivacyService(cfg) → 隐私计算引擎（脱敏/DP/K-匿名/...）
├── observability.NewEngineMetrics() → Prometheus 指标收集器
└── naming.SetObserver(...) → 注册命名观测器（别名/归一化事件）

阶段 3：REST API 构建
├── gin.New() → ReleaseMode
├── 中间件链（按执行顺序）：
│   ├── gin.Recovery()              → panic 恢复
│   ├── TraceMiddleware()           → X-Request-ID + X-Trace-ID
│   ├── SecurityHeadersMiddleware() → CSP/HSTS/X-Frame-Options
│   ├── AuthMiddleware()            → API Key 认证
│   ├── RateLimitMiddleware()       → 32 分片令牌桶限流
│   ├── RequestLogger()             → 请求日志
│   └── PrometheusMiddleware()      → RED 指标埋点
├── rest.RegisterRoutes(router, svc) → 注册全部隐私原语 API
└── router.GET("/metrics", ...)      → Prometheus 抓取端点

阶段 4：REST Server 启动
├── http.Server{ReadTimeout: 30s, WriteTimeout: 30s, IdleTimeout: 120s}
├── TLS 模式：ListenAndServeTLS(cert, key)
└── 非 TLS 模式：ListenAndServe()

阶段 5：gRPC Server 构建
├── Keepalive 配置：
│   ├── MaxConnectionIdle: 5m    → 空闲连接超时
│   ├── MaxConnectionAge: 2h     → 连接最大生命周期
│   ├── Time: 2m                 → Ping 间隔
│   └── Timeout: 20s             → Ping 超时
├── TLS/mTLS 凭证（如启用）
├── mTLS CN 白名单拦截器（如配置白名单文件）
│   ├── NewWhitelistInterceptor(path) → 一元 + 流式拦截器
│   └── ChainUnaryInterceptor + ChainStreamInterceptor
└── grpcserver.NewServer(svc, opts...) → 注册 PrivacyService

阶段 6：启动配置摘要
└── slog.Info("Configuration summary", ...)
    ├── 监听地址、TLS/mTLS 状态
    ├── 认证状态、限流参数
    └── 隐私预算总量/剩余量

阶段 7：信号处理与优雅停机
├── signal.Notify(quit, SIGINT, SIGTERM)
├── 收到信号 → rest.SetReady(false) → K8s 就绪探针失败
├── 流量排空等待（PRIVACY_SHUTDOWN_DRAIN_SECONDS, 默认 5s）
├── REST 优雅停止（30s 超时）
└── gRPC 优雅停止（15s 超时 → 强制停止回退）
```

### P0-1 零信任门禁 Validate() 设计分析

门禁是整个安全体系的第一道防线，其核心哲学是：**宁可拒绝启动，不可静默降级**。

```go
// engine-go/internal/config/ 中 Validate() 的检查逻辑（简化）
func (c *Runtime) Validate() error {
    // 检查 1：非环回监听必须配置凭据
    if !c.isLoopback() && !c.hasCredentials() {
        return errors.New("non-loopback bind without authentication")
    }
    // 检查 2：声明必须加密但未启用 TLS
    if c.RequireTLS && !c.TLSEnabled {
        return errors.New("PRIVACY_REQUIRE_TLS=true but TLS not enabled")
    }
    // 检查 3：启用 mTLS 但白名单文件不存在
    if c.MTLSEnabled && !fileExists(c.MTLSWhitelistFile) {
        return errors.New("mTLS enabled but whitelist file missing")
    }
    return nil
}
```

**设计决策解读**：
- **为何用 `log.Fatalf` 而非返回 error**：`main()` 中直接 `os.Exit(1)`，确保进程不会在缺少安全配置的情况下运行
- **为何环回地址可以无密钥**：开发环境便利性与安全性的平衡。但启动时会输出 WARN 级别日志提醒
- **`AuthEffectivelyEnabled()`**：综合判断认证是否「实际生效」——即使 `AUTH_ENABLED=false`，如果配置了 API Key 或 mTLS，也视为认证已启用

### Gateway 启动全流程

**源码位置**：`engine-go/cmd/privshield-gateway/main.go` (181 行)

网关是纯流量治理层，启动流程比 Agent 简单：

```
阶段 1：配置加载 + 门禁
├── engineconfig.LoadGateway()
└── cfg.Validate() → 网关不终止 TLS，鉴权由后端 Agent 承担

阶段 2：组件初始化
├── 解析 GATEWAY_BACKENDS（逗号分隔的后端地址列表）
├── gateway.NewLoadBalancer(addresses, strategy) → P2C/RR/LC 负载均衡器
└── observability.NewGatewayMetrics() → 网关专用 Prometheus 指标

阶段 3：HTTP 反向代理
├── Gin 中间件：Recovery → RequestLogger → PrometheusMiddleware
├── 内置路由：
│   ├── GET /health              → 网关自身健康检查
│   ├── GET /gateway/backends    → 后端状态查询
│   └── GET /metrics             → Prometheus 指标（可选 Bearer Token 保护）
└── NoRoute(gateway.NewHTTPProxyHandler(lb, gwMetrics)) → 反向代理

阶段 4：gRPC 透明流代理
└── gateway.NewGrpcProxyListener(lb, grpcAddr, gwMetrics)
    ├── grpc.UnknownServiceHandler → 拦截所有未注册方法
    └── grpc.CustomCodec(rawCodec{}) → 字节透传

阶段 5：优雅停机
├── HTTP 优雅停止（15s 超时）
└── gRPC 优雅停止（10s 超时 → 强制停止）
```

**网关与 Agent 的关键差异**：

| 维度 | Agent | Gateway |
|---|---|---|
| **中间件链** | 7 层（安全头→追踪→认证→限流→...） | 3 层（Recovery→Logger→Metrics） |
| **认证** | API Key + mTLS CN 白名单 | 无（由后端 Agent 负责） |
| **路由** | 注册全部隐私原语 API | NoRoute 全转发 + 少量管理端点 |
| **gRPC 处理** | 注册 PrivacyService 实现 | UnknownServiceHandler 透明代理 |
| **停机排空** | 5s 排空 + 30s 超时 | 无排空 + 15s 超时 |
| **指标** | EngineMetrics（RED + 5 业务指标） | GatewayMetrics（转发/InFlight/EWMA/熔断器） |

### 安全配置加载链路

**源码位置**：`engine-go/internal/security/config.go` (143 行)

安全配置通过 `sync.Once` 单例模式加载，确保整个进程生命周期内只解析一次环境变量：

```go
// engine-go/internal/security/config.go L22-47
type Settings struct {
    pkgauth.Settings                    // 嵌入基础认证配置
    RateLimitEnabled      bool          // 限流开关
    HealthNoRateLimit     bool          // 健康端点免限流
    RateLimitDefaultRPS   float64       // 默认 RPS
    RateLimitDefaultBurst int           // 默认突发容量
    RateLimitPerEndpoint  map[string]*EndpointRateLimit  // 按端点限流
    RateLimitRedisURL     string        // Redis 共享限流（预留）
    MTLSAllowedCNs        []string      // 静态 CN 白名单
    MTLSWhitelistFile     string        // 动态 CN 白名单文件路径
    MTLSEnabled           bool          // mTLS 开关
}

var (
    settingsOnce   sync.Once      // 确保只加载一次
    cachedSettings *Settings      // 缓存的配置单例
)

func GetSettings() *Settings {
    settingsOnce.Do(func() {
        cachedSettings = loadSettings()
    })
    return cachedSettings
}
```

**API Key 解析格式**：

```bash
# 环境变量格式：key:name:scope1,scope2;key2:name2:scope3
# 示例：
PRIVACY_AUTH_INTERNAL_API_KEYS="sk-abc123:service-hub:/v1/privacy/*,/v1/classify/*;sk-def456:bff-go:*"
```

解析逻辑（`parseAPIKeys`）：
1. 按 `;` 分割多个 Key 条目
2. 每条按 `:` 分割为 `token:name:scopes` 三段
3. scopes 按 `,` 分割为权限列表
4. 缺少 scopes 时默认 `["*"]`（全部权限）

**设计要点**：
- 内部 Key 和外部 Key 分别存储在不同的 map 中，认证时分别执行常量时间查找
- `PRIVACY_AUTH_API_KEY` 和 `PRIVACY_API_KEY` 是简化格式的兼容别名（自动赋予 `default-internal` 名称和 `*` 权限）
- `ResetSettings()` 仅在测试中使用，重置 `sync.Once` 以允许重新加载

### 优雅停机设计分析

优雅停机是生产级服务的必备能力。Agent 的停机流程经过精心设计：

```
SIGINT/SIGTERM
    │
    ▼
① rest.SetReady(false)          → K8s 就绪探针返回失败
    │                                → Service Endpoints 摘除
    │                                → 新流量不再路由到此 Pod
    ▼
② 流量排空等待 (5s)             → 等待 in-flight 请求完成
    │                                → 已建立的连接继续处理
    │                                → 新请求被 K8s 路由到其他 Pod
    ▼
③ restServer.Shutdown(30s)      → 停止接受新连接
    │                                → 等待活跃请求完成
    │                                → 超时后强制关闭
    ▼
④ gRPC GracefulStop(15s)        → 发送 GOAWAY
    │                                → 等待活跃 RPC 完成
    │   ┌─────────────────────────┘
    │   │ 超时回退
    │   ▼
⑤ grpcSrv.Stop()                → 强制关闭所有连接
    │
    ▼
  进程退出
```

**为何需要排空等待？**

K8s 的 Endpoints 更新和 iptables 规则传播有延迟（通常 1-3 秒）。如果不等待就直接关闭服务器，在 Endpoints 更新传播期间仍可能有少量流量到达，导致连接被拒绝。5 秒的排空窗口足以覆盖这个传播延迟。

**gRPC 为何需要超时回退？**

`GracefulStop()` 会等待所有活跃 RPC 完成。如果某个客户端的流式 RPC 长时间不结束（如 gRPC 双向流代理），`GracefulStop()` 会无限阻塞。15 秒超时后调用 `Stop()` 强制关闭，防止进程挂死。

---

## 第 10 章：gRPC 透明代理完整代码走读

gRPC 透明代理是网关中最复杂的组件（310 行），理解它需要掌握 gRPC 底层流式通信模型。

### rawCodec 零编解码设计

**源码位置**：`engine-go/internal/gateway/grpc_proxy.go` (L31-52)

标准 gRPC 代理需要先反序列化客户端消息为 protobuf 对象，再重新序列化为字节发送给后端。`rawCodec` 跳过了这个「解码 → 编码」过程：

```go
// engine-go/internal/gateway/grpc_proxy.go L31-52
type rawCodec struct{}

func (rawCodec) Marshal(v interface{}) ([]byte, error) {
    if b, ok := v.(*[]byte); ok {
        return *b, nil  // 直接返回原始字节，不做编码
    }
    return nil, fmt.Errorf("rawCodec: unsupported type %T", v)
}

func (rawCodec) Unmarshal(data []byte, v interface{}) error {
    if b, ok := v.(*[]byte); ok {
        *b = data  // 直接存储原始字节，不做解码
        return nil
    }
    return fmt.Errorf("rawCodec: unsupported type %T", v)
}

func (rawCodec) Name() string { return "raw" }
```

**为何使用 `*[]byte` 而非 `[]byte`？**

gRPC 的 `RecvMsg` 和 `SendMsg` 接口接受 `interface{}` 参数。使用 `*[]byte` 允许 `Unmarshal` 修改调用方持有的字节切片引用，避免额外的内存拷贝。这是零拷贝透传的关键。

### 连接池管理：双重检查锁 + 健康检查

**源码位置**：`engine-go/internal/gateway/grpc_proxy.go` (L82-130)

```go
// getOrCreateConn 双重检查锁模式
func (g *GrpcProxyServer) getOrCreateConn(addr string) (*grpc.ClientConn, error) {
    // 第一次检查（读锁，快速路径）
    g.connPoolMu.RLock()
    conn, ok := g.connPool[addr]
    g.connPoolMu.RUnlock()
    if ok && isConnReady(conn) {
        return conn, nil  // 连接已存在且健康 → 直接返回
    }

    // 加写锁（慢路径）
    g.connPoolMu.Lock()
    defer g.connPoolMu.Unlock()

    // 第二次检查（防止并发创建）
    conn, ok = g.connPool[addr]
    if ok && isConnReady(conn) {
        return conn, nil
    }

    // 关闭旧连接
    if ok && conn != nil {
        _ = conn.Close()
        delete(g.connPool, addr)
    }

    // 连接池容量保护
    if len(g.connPool) >= g.maxPoolSize {
        return nil, fmt.Errorf("connection pool full (max %d)", g.maxPoolSize)
    }

    // 创建新连接
    ctx, cancel := context.WithTimeout(context.Background(), g.dialTimeout)
    defer cancel()

    conn, err := grpc.DialContext(ctx, addr,
        grpc.WithTransportCredentials(insecure.NewCredentials()),
        grpc.WithDefaultCallOptions(grpc.ForceCodec(rawCodec{})),
    )
    if err != nil {
        return nil, fmt.Errorf("dial backend %s: %w", addr, err)
    }
    g.connPool[addr] = conn
    return conn, nil
}
```

**连接健康检查**（`isConnReady`）：

```go
func isConnReady(conn *grpc.ClientConn) bool {
    s := conn.GetState().String()
    return s == "READY" || s == "IDLE" || s == "CONNECTING"
    // IDLE 和 CONNECTING 也视为可用：连接池应复用，而非反复创建
    // 仅 TRANSIENT_FAILURE / SHUTDOWN 视为不可用
}
```

**设计决策**：
- `maxPoolSize: 256`：防止后端地址动态变化时连接数无限增长（内存泄漏保护）
- `dialTimeout: 5s`：避免后端不可达时连接创建阻塞过久
- `insecure.NewCredentials()`：当前内网部署不加密，生产环境应升级为 mTLS 回源（已标记为 P2 改进项）

### 双向流转发时序图

```
客户端                网关 (GrpcProxyServer)              后端 Agent
  │                          │                              │
  │── gRPC Stream ──────────▶│                              │
  │                          │── SelectNode() ──▶ P2C-EWMA  │
  │                          │── getOrCreateConn() ─────────▶│
  │                          │                              │
  │                          │── conn.NewStream() ──────────▶│
  │                          │◀──── stream established ─────│
  │                          │                              │
  │                          │  ┌── goroutine 1 ──┐         │
  │◀── frame ────────────────│──│ serverStream.RecvMsg()    │
  │                          │  │ clientStream.SendMsg() ──▶│
  │── frame ─────────────────▶│  │ serverStream.RecvMsg()    │
  │                          │──│ clientStream.SendMsg() ──▶│
  │                          │  └───────────────────┘        │
  │                          │                              │
  │                          │  ┌── goroutine 2 ──┐         │
  │                          │──│ clientStream.RecvMsg() ◀──│
  │◀── frame ────────────────│  │ serverStream.SendMsg()    │
  │                          │──│ clientStream.RecvMsg() ◀──│
  │◀── frame ────────────────│  │ serverStream.SendMsg()    │
  │                          │  └───────────────────┘        │
  │                          │                              │
  │── EOF ───────────────────▶│                              │
  │                          │── clientStream.CloseSend() ──▶│
  │                          │                              │── EOF
  │                          │◀──── both goroutines exit ───│
  │                          │                              │
  │                          │── UpdateEWMA(latency)        │
  │                          │── CB.RecordSuccess/Failure() │
  │                          │── metrics 上报                │
```

**关键实现细节**：

1. **`errChan` 容量为 2**：两个 goroutine 各写入一个 error，不会阻塞
2. **`streamCancel()`**：第一个 goroutine 退出时调用 cancel，通知另一个 goroutine 也退出
3. **`<-errChan` 两次**：确保两个 goroutine 都退出后才继续，防止 goroutine 泄漏
4. **metadata 透传**：`metadata.FromIncomingContext` + `metadata.NewOutgoingContext` 确保 TraceID 等上下文信息传递到后端

---

## 第 11 章：网关部署拓扑与运维指南

### Docker Compose 部署拓扑

本项目提供多种 Docker Compose 编排文件，覆盖不同部署场景：

```
deploy/docker-compose/
├── docker-compose.yml                    # 基础编排（Python 引擎）
├── docker-compose.dev.yml                # 开发环境（无认证）
├── docker-compose.go-engine.yml          # Go 引擎编排
├── docker-compose.dev-go-engine.yml      # Go 引擎开发环境
├── docker-compose.mtls.yml               # mTLS 双向认证
├── docker-compose.mtls-go-engine.yml     # Go 引擎 + mTLS
├── docker-compose.prod.yml               # 生产环境
├── docker-compose.prod-go-engine.yml     # Go 引擎生产环境
├── docker-compose.test.yml               # 测试环境
├── docker-compose.app-lz.yml             # 应用 LZ 场景
└── .env.example / .env.prod.example      # 环境变量模板
```

**典型开发环境拓扑**（`docker-compose.dev-go-engine.yml`）：

```
┌──────────────────────────────────────────────────────────┐
│                    Docker Network                         │
│                                                          │
│  ┌─────────────────┐    ┌─────────────────┐             │
│  │ privshield-agent │    │ privshield-bff   │             │
│  │ :8079 (REST)     │◀───│ :8081 (HTTPS)    │             │
│  │ :50051 (gRPC)    │    │                  │             │
│  └─────────────────┘    └─────────────────┘             │
│                                                          │
│  ┌─────────────────┐    ┌─────────────────┐             │
│  │ privshield-web   │    │ prometheus       │             │
│  │ :5173 (Vite)     │    │ :9090            │             │
│  └─────────────────┘    └─────────────────┘             │
│                                                          │
└──────────────────────────────────────────────────────────┘
```

**生产环境拓扑**（`docker-compose.prod-go-engine.yml`）：

```
┌──────────────────────────────────────────────────────────┐
│                    Docker Network                         │
│                                                          │
│  ┌──────────────────────────────────────────┐            │
│  │ privshield-gateway (:8000 + :50000)       │            │
│  │  ├── HTTP 反向代理 (P2C-EWMA)             │            │
│  │  └── gRPC 透明流代理                       │            │
│  └──────────────────┬───────────────────────┘            │
│                     │                                    │
│  ┌──────────┐  ┌──────────┐  ┌──────────┐              │
│  │ Agent-1  │  │ Agent-2  │  │ Agent-3  │  ← N 副本    │
│  │ :8079    │  │ :8079    │  │ :8079    │              │
│  │ :50051   │  │ :50051   │  │ :50051   │              │
│  └──────────┘  └──────────┘  └──────────┘              │
│                                                          │
│  ┌─────────────────┐    ┌─────────────────┐             │
│  │ service-hub      │    │ audit-log        │             │
│  │ :8082            │    │ :8084            │             │
│  └─────────────────┘    └─────────────────┘             │
│                                                          │
│  ┌─────────────────┐    ┌─────────────────┐             │
│  │ prometheus       │    │ grafana          │             │
│  │ :9090            │    │ :3000            │             │
│  └─────────────────┘    └─────────────────┘             │
└──────────────────────────────────────────────────────────┘
```

### K8s 部署架构

```
deploy/k8s/
├── namespace.yaml           # privshield Namespace
├── configmap.yaml           # 隐私策略配置
├── secret.example.yaml      # TLS 证书 + API Key 模板
├── deployment-go.yaml       # Go Agent Deployment
├── service-go.yaml          # Go Agent Service (ClusterIP)
├── kustomization-go.yaml    # Kustomize 叠加配置
├── llm-deployment.yaml      # LLM 推理服务（可选）
└── llm-service.yaml         # LLM 服务 Service
```

**K8s 健康探针配置**：

```yaml
# deployment-go.yaml 中的探针配置
readinessProbe:
  httpGet:
    path: /readyz
    port: 8079
  initialDelaySeconds: 5
  periodSeconds: 10
  failureThreshold: 3

livenessProbe:
  httpGet:
    path: /healthz
    port: 8079
  initialDelaySeconds: 10
  periodSeconds: 15
  failureThreshold: 5
```

- `/readyz`：就绪探针。返回 200 表示可以接收流量。优雅停机时 `rest.SetReady(false)` 会让此端点返回 503
- `/healthz`：存活探针。返回 200 表示进程正常运行

### 性能调优参数速查

| 参数 | 默认值 | 调优建议 |
|---|---|---|
| `GOMAXPROCS` | CPU 核数 | 容器内通常需手动设置或安装 `go.uber.org/automaxprocs` |
| `GOGC` | `100` | 高 QPS 场景可调至 `200` 减少 GC 频率 |
| `sharedTransport.MaxIdleConns` | `2048` | 后端数量 × 256 |
| `sharedTransport.MaxIdleConnsPerHost` | `256` | 单后端峰值并发连接数 |
| `sharedTransport.IdleConnTimeout` | `90s` | 连接复用率 vs 资源占用平衡 |
| `connPool.maxPoolSize` (gRPC) | `256` | 后端数量 × 32 |
| `connPool.dialTimeout` (gRPC) | `5s` | 后端不可达容忍时间 |
| `numRateLimitShards` | `32` | CPU 核数的 2-4 倍 |
| `cleanup interval` (限流) | `3m` | 内存占用 vs 清理频率 |
| `TTL` (限流桶) | `10m` | 用户会话持续时间 |
| `EWMA alpha` (HTTP) | `0.3` | 越大越敏感近期延迟 |
| `EWMA alpha` (gRPC) | `0.2` | 更平滑，避免单次抖动影响选择 |
| `MaxConcurrent` | `1000` | 进程级最大并发连接数 |
| `MaxBodySize` | `32MiB` | 最大请求体大小 |

### pprof 性能分析端点

当 `PRIVACY_PPROF_ENABLED=true` 时，Agent 暴露标准 Go pprof 端点：

```bash
# CPU 分析（30 秒采样）
curl -o cpu.prof http://localhost:8079/debug/pprof/profile?seconds=30
go tool pprof cpu.prof

# 内存分析
curl -o mem.prof http://localhost:8079/debug/pprof/heap
go tool pprof mem.prof

# Goroutine 分析
curl http://localhost:8079/debug/pprof/goroutine?debug=1

# 阻塞分析
curl -o block.prof http://localhost:8079/debug/pprof/block
go tool pprof block.prof
```

**安全注意**：pprof 端点在生产环境默认关闭。开启时需要 `ops:admin` 权限，因为 pprof 数据可能泄露内部实现细节。

---

## 第 12 章：Review 检查清单详细版

以下清单为每个模块的具体检查点，Review 时逐项确认。

### 网关模块检查清单

#### 负载均衡器 (`pkg/gateway/balancer.go`)

- [ ] P2C 算法：随机选择两个不同节点（`j != i` 循环）
- [ ] P2C 负载评分：`(InFlight+1) * max(EWMA, 0.001)`，理解 +1 和下限的防除零作用
- [ ] SWRR 算法：`currentWeight += weight` → 选最大 → `currentWeight -= totalWeight`
- [ ] SWRR 原子操作：`n.currentWeight.Add(int32(n.Weight))` 确保并发安全
- [ ] 全部熔断时返回 `nodes[0]` 而非 nil 的设计意图
- [ ] `ReverseProxy()` 使用 `sync.Once` 确保每个节点只创建一次代理
- [ ] `BufferPool.Put()` 检查 `cap(b) >= 32*1024` 防止小缓冲区污染
- [ ] `DecrementInFlight()` CAS 循环防止负数
- [ ] `UpdateEWMA()` 公式正确性：`alpha*latency + (1-alpha)*old`

#### 熔断器 (`pkg/circuitbreaker/circuitbreaker.go`)

- [ ] 三态转换：Closed → Open（failures >= threshold）
- [ ] Open → HalfOpen（冷却期 `cooldown` 过后）
- [ ] HalfOpen → Closed（成功探测 `halfOpenMax` 次 = 3）
- [ ] HalfOpen → Open（任何一次失败立即回退）
- [ ] `RecordFailure()` 每次更新 `openedAt` 的影响（保守策略）
- [ ] `StateString()` 返回可读状态名用于指标上报

#### gRPC 代理 (`engine-go/internal/gateway/grpc_proxy.go`)

- [ ] `rawCodec` 实现 `grpc.encoding.Codec` 四个方法
- [ ] `getOrCreateConn()` 双重检查锁模式正确性
- [ ] `isConnReady()` 接受 IDLE/READY/CONNECTING 三种状态
- [ ] `maxPoolSize: 256` 防止连接泄漏
- [ ] `TransparentStreamDirector()` 完整流程：选节点 → 建连 → 双向流 → 指标上报
- [ ] metadata 透传：`FromIncomingContext` → `NewOutgoingContext`
- [ ] `errChan` 容量为 2，两个 goroutine 各写一个
- [ ] `streamCancel()` 确保两个 goroutine 都能退出
- [ ] `<-errChan` 两次等待确保无 goroutine 泄漏

### 安全模块检查清单

#### 认证中间件 (`pkg/auth/middleware.go`)

- [ ] `ConstantTimeLookup()` 排序 key 消除 map 迭代随机性
- [ ] 不提前 break — 始终比较所有 key
- [ ] 内部和外部 key 分别执行常量时间查找
- [ ] 认证未启用时注入匿名身份（`ServiceType: "anonymous"`）
- [ ] 健康端点路径（`/health`, `/readyz`, `/healthz`）免认证

#### 权限模型 (`pkg/auth/identity.go`)

- [x] `PermissionForRESTPath()` 路径归一化：`/api/v1/*` → `/v1/*`，尾部斜杠去除
- [x] `PermissionForRESTPath()` 别名路由全覆盖：根路径别名 + 快捷别名（`/v1/mask*`、`/v1/dp/*` 等）
- [x] `PermissionForRESTPath()` 动态分类读写区分：`dynclassification:write` vs `dynclassification:read`
- [x] `PermissionForRESTPath()` `budget/reset` 映射到 `privacy:budget`
- [x] `PermissionForGRPCMethod()` 方法到权限的映射完整性（44 个隐私原语）
- [x] `ServiceHubPermissionForPath()` service-hub 专属路径映射（`hub:read` / `hub:dispatch`）
- [x] `ParseAPIKeysEnv()` 共享解析器：TrimSpace + 空 token 丢弃 + 默认 `["*"]`
- [x] `HasPermission()` 支持 `"*"` 通配符
- [x] `admin` scope 拥有所有权限

#### 限流器 (`pkg/middleware/ratelimit.go`)

- [ ] FNV-1a 哈希分片：`hash % 32` 均匀分布
- [ ] 令牌桶实现：`tokens += elapsed * rps; tokens = min(tokens, burst)`
- [ ] 路径归一化：`NormalizeRateLimitPath()` 替换动态 ID 段
- [ ] 匿名调用者追加客户端 IP 作为分片因子
- [ ] 后台清理协程：3 分钟间隔，10 分钟 TTL
- [ ] `MaxConcurrent` Channel 信号量模式
- [ ] 停机信号 `stopCh` 正确传播

#### mTLS 白名单 (`pkg/tlsutil/whitelist.go`)

- [ ] 双格式兼容：`clients` 键（标准）和 `entries` 键（历史）
- [ ] Scope 匹配规则：`"*"` → 精确匹配 → 前缀通配符
- [ ] 后台 5s 轮询 mtime 检测
- [ ] `Close()` 优雅停机：`stopMu` 保护 + 幂等调用
- [ ] 加载失败保留旧配置（不清空 `clients` map）

### 可观测性模块检查清单

#### Prometheus 指标 (`pkg/observability/metrics.go`)

- [ ] 独立 `prometheus.Registry`（非全局默认）
- [ ] `/metrics` 自身豁免（防止自引用无限增长）
- [ ] `PrometheusMiddleware()` 使用 `c.FullPath()` 而非 `c.Request.URL.Path`
- [ ] gRPC `UnaryServerInterceptor` 正确埋点
- [ ] 指标命名统一 `privshield_` 前缀

#### 追踪 (`pkg/observability/trace.go`)

- [ ] `GenerateRequestID()` 4 级防空兜底
- [ ] `TraceMiddleware()` 复用 `X-Request-ID` 或生成新 ID
- [ ] 响应头写入 `X-Request-ID` 和 `X-Trace-ID`
- [ ] `GetTraceID()` 优先从 context 获取，回退到请求头

#### 结构化日志 (`pkg/observability/logger.go`)

- [ ] JSON 格式输出（生产环境可解析）
- [ ] 日志级别从 `PRIVACY_LOG_LEVEL` 环境变量读取
- [ ] 级别解析失败默认 INFO（不报错）

---

## 第 13 章：周交付物清单

### 交付物 1：engine-go 网关与安全模块 Review 笔记

- [ ] P2C-EWMA 负载均衡算法理解笔记（含数学推导）
- [ ] 熔断器状态机转换图（标注所有转换条件）
- [ ] gRPC 透明代理双向流转发时序图
- [ ] BufferPool 零分配机制分析报告
- [ ] mTLS CN 白名单热重载机制对比分析（engine-go vs pkg 两套实现）
- [ ] API Key 常量时间认证安全性分析
- [ ] 32 分片令牌桶限流性能分析

### 交付物 2：发现的问题清单与改进建议

| 优先级 | 问题 | 位置 | 状态 | 建议 |
|---|---|---|---|---|
| **P0** | **SEC-09**: `PermissionForRESTPath` 别名路由权限绕过（40+ 路由） | `pkg/auth/identity.go` | **已修复** | 路径归一化 + 全量 case 覆盖 |
| **P0** | **SEC-10**: service-hub 缺少 Scope-based 细粒度鉴权 | `services/service-hub/` | **已修复** | `scopeAuthMiddleware` 双模式 + `ServiceHubPermissionForPath` |
| **P1** | **SEC-11**: `parseAPIKeys` 重复实现逻辑漂移 | `engine-go/internal/security/config.go` | **已修复** | 共享 `ParseAPIKeysEnv` 到 `pkg/auth` |
| **P1** | **SEC-12**: 动态分类分级读写权限未区分 | `pkg/auth/identity.go` | **已修复** | `dynclassification:write` vs `dynclassification:read` |
| **P1** | **SEC-13**: `/v1/privacy/budget/reset` 权限映射缺失 | `pkg/auth/identity.go` | **已修复** | 新增 `privacy:budget` 映射 |
| **P0** | **G-03**: 缺少登录失败处理与账号锁定 | `console/app-lz/bff-go/internal/auth/userstore.go` | **已修复** | 5 次失败锁定 15 分钟 |
| **P0** | **G-04**: 密码策略过于宽松 | `console/app-lz/bff-go/internal/auth/userstore.go` | **已修复** | 12 位 + 3 类字符 + 弱密码字典 |
| **P0** | **G-06**: SM2 非对称密码算法 | `pkg/crypto/sm2.go` | **已修复** | 纯 Go SM2 签名/验签/加密/解密 |
| **P1** | **G-05**: 缺少 JWT 令牌吊销 | `console/app-lz/bff-go/internal/auth/jwt.go` | **已修复** | sync.Map 黑名单 + 自动清理 |
| **P1** | **G-11**: 缺少特权用户双因素认证 | `console/app-lz/bff-go/internal/auth/totp.go` | **已修复** | RFC 6238 TOTP |
| **P1** | **G-12**: 缺少 WAF 攻击检测 | `pkg/middleware/waf.go` | **已修复** | 5 类 72 条正则规则 |
| **P1** | **G-14**: API Key 无过期机制 | `pkg/auth/settings.go` + `identity.go` | **已修复** | ExpiresAt + IsExpired |
| **P1** | **G-02**: X-Forwarded-For 注入 | `pkg/middleware/middleware.go` | **已修复** | TrustedProxies + RealClientIP |
| P1 | `OTelTracer.StartSpan()` 为 no-op | `pkg/observability/tracing.go:32` | 待修复 | 引入 OTel SDK 实现真实 Span 创建 |
| P1 | 缺少 Prometheus 告警规则 | `deploy/prometheus/` | 待修复 | 补充 P99/错误率/熔断器/证书过期告警 |
| P2 | gRPC 代理使用 `insecure` 凭证 | `grpc_proxy.go:114` | 待修复 | 升级为 mTLS 回源 |
| P2 | 熔断器 `halfOpenMax` 硬编码 | `circuitbreaker.go:62` | 待修复 | 配置化 |
| P2 | 两套 mTLS 白名单实现语义漂移 | `security/whitelist.go` vs `tlsutil/whitelist.go` | 待修复 | 收敛为统一实现 |
| P3 | API Key 无过期/轮转机制 | `pkg/auth/middleware.go` | 待修复 | 生产环境需要 Key 轮转能力 |
| P3 | 限流状态纯内存，多实例不共享 | `pkg/middleware/ratelimit.go` | 待评估 | 评估 Redis 共享限流需求 |

### 交付物 3：可观测性改进方案

- [ ] 分类漏斗三阶段创建子 Span（Rule → NER → LLM）
- [ ] 审计关键事件标准化日志格式
- [ ] Prometheus 告警规则补充（`deploy/prometheus/rules/`）
- [ ] Grafana 网关监控看板（InFlight/EWMA/熔断器状态）

---

## 附录 A：关键环境变量速查表

| 环境变量 | 默认值 | 影响模块 | 说明 |
|---|---|---|---|
| `PRIVACY_AUTH_ENABLED` | `false` | auth | 启用 API Key 认证 |
| `PRIVACY_TLS_ENABLED` | `false` | auth | 启用 TLS |
| `PRIVACY_AUTH_INTERNAL_MTLS_ENABLED` | `false` | security | 启用 gRPC mTLS |
| `PRIVACY_AUTH_MTLS_WHITELIST_FILE` | — | security | CN 白名单 YAML 路径 |
| `PRIVACY_AUTH_MTLS_ALLOWED_CNS` | — | security | 静态 CN 白名单（逗号分隔） |
| `PRIVACY_AUTH_INTERNAL_API_KEYS` | — | security | 内部 API Key（格式 `key:name:scope1,scope2`） |
| `PRIVACY_AUTH_EXTERNAL_API_KEYS` | — | security | 外部 API Key |
| `SERVICE_HUB_API_KEYS` | — | service-hub | Scope-based 多密钥（格式 `key:name:scope1,scope2`） |
| `SERVICE_HUB_API_KEY` | — | service-hub | 单密钥兼容模式（回退） |
| `PRIVACY_RATE_LIMIT_ENABLED` | `false` | security | 启用 32 分片限流 |
| `PRIVACY_RATE_LIMIT_DEFAULT_RPS` | `100` | security | 默认每秒请求数 |
| `PRIVACY_RATE_LIMIT_DEFAULT_BURST` | `200` | security | 默认突发容量 |
| `PRIVACY_PPROF_ENABLED` | `false` | security | 启用 pprof 端点 |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | observability | OTel Collector 端点 |
| `PRIVACY_SERVICE_NAME` | `PrivShield` | observability | 服务名称（追踪标签） |

---

## 附录 B：核心数据结构关系图

```
┌─────────────────────────────────────────────────────────────────┐
│                        LoadBalancer                             │
│  ├── nodes: []*BackendNode                                      │
│  ├── strategy: string ("p2c" | "round_robin" | ...)             │
│  └── rrIndex: atomic.Int32                                      │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  BackendNode                  circuitbreaker.Breaker            │
│  ├── Address: string          ├── state: State                  │
│  ├── Weight: int              ├── failures: int                 │
│  ├── InFlight: atomic.Int64   ├── threshold: int                │
│  ├── EWMA: float64            ├── cooldown: time.Duration       │
│  ├── CB: *Breaker ──────────▶ ├── openedAt: time.Time           │
│  ├── proxyOnce: sync.Once     └── mu: sync.Mutex                │
│  └── proxy: *ReverseProxy                                       │
│                                                                 │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  sharedTransport (http.Transport)    byteBufferPool (sync.Pool) │
│  ├── MaxIdleConns: 2048              ├── 32KB 预分配缓冲区       │
│  ├── MaxIdleConnsPerHost: 256        └── cap >= 32KB 才回收      │
│  └── IdleConnTimeout: 90s                                     │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

---

## 附录 C：安全认证链路全景图

```
                          HTTP 请求
                             │
                             ▼
    ┌────────────────────────────────────────────┐
    │ ① SecurityHeadersMiddleware               │
    │    → CSP, HSTS, X-Frame-Options           │
    └──────────────────┬─────────────────────────┘
                       │
                       ▼
    ┌────────────────────────────────────────────┐
    │ ② TraceMiddleware                          │
    │    → 生成/复用 TraceID                      │
    │    → 写入 X-Request-ID / X-Trace-ID 响应头  │
    └──────────────────┬─────────────────────────┘
                       │
                       ▼
    ┌────────────────────────────────────────────┐
    │ ③ MaxBodySize(32MiB) + MaxConcurrent(1000) │
    │    → DDoS 第 1/2 层防护                    │
    └──────────────────┬─────────────────────────┘
                       │
                       ▼
    ┌────────────────────────────────────────────┐
    │ ④ RateLimit(rps, burst)                    │
    │    → 32 分片令牌桶 (DDoS 第 3 层)           │
    └──────────────────┬─────────────────────────┘
                       │
                       ▼
    ┌────────────────────────────────────────────┐
    │ ⑤ AuthMiddleware / AuthWithRoles           │
    │    → ExtractBearerToken                    │
    │    → ConstantTimeLookup (内部 Key → 外部 Key)│
    │    → PermissionForRESTPath → HasPermission  │
    └──────────────────┬─────────────────────────┘
                       │
                       ▼
    ┌────────────────────────────────────────────┐
    │ ⑥ mTLS CN 白名单拦截器 (gRPC 侧)           │
    │    → extractClientCN(ctx)                  │
    │    → DynamicWhitelist.CheckScope(cn, method)│
    └──────────────────┬─────────────────────────┘
                       │
                       ▼
                  业务 Handler
```

---

## 附录 D：推荐阅读与延伸阅读

### 算法与论文

| 主题 | 资料 | 说明 |
|---|---|---|
| P2C 负载均衡 | [The Power of Two Choices in Randomized Load Balancing](https://www.eecs.harvard.edu/~michaelm/postscripts/tpds2001.pdf) | Mitzenmacher et al. 经典论文，证明 P2C 将最大负载从 O(log n) 降到 O(log log n) |
| EWMA | [Exponentially Weighted Moving Average](https://en.wikipedia.org/wiki/Exponential_smoothing) | 指数移动加权平均的数学基础 |
| Nginx SWRR | [Nginx 加权轮询算法](https://blog.csdn.net/yangbh12/article/details/105550857) | 平滑加权轮询的原始设计 |
| 令牌桶 | [Token Bucket Algorithm](https://en.wikipedia.org/wiki/Token_bucket) | 经典限流算法，与漏桶算法的对比 |
| 熔断器模式 | [Circuit Breaker Pattern](https://learn.microsoft.com/en-us/azure/architecture/patterns/circuit-breaker) | Microsoft Azure 架构指南中的熔断器模式详解 |

### Go 并发与性能

| 主题 | 资料 | 说明 |
|---|---|---|
| sync.Pool | [Go 官方文档 sync.Pool](https://pkg.go.dev/sync#Pool) | 注意 GC 会清空池对象 |
| atomic CAS | [Go sync/atomic](https://pkg.go.dev/sync/atomic) | CompareAndSwap 循环模式 |
| 时序攻击 | [crypto/subtle](https://pkg.go.dev/crypto/subtle) | 常量时间比较的密码学基础 |

### 可观测性

| 主题 | 资料 | 说明 |
|---|---|---|
| RED 方法论 | [The RED Method](https://www.weave.works/blog/the-red-method) | Tom Wilkie 提出的微服务指标方法论 |
| Prometheus 指标类型 | [Prometheus 文档 - Metric Types](https://prometheus.io/docs/concepts/metric_types/) | Counter/Gauge/Histogram/Summary |
| OpenTelemetry | [OpenTelemetry Go SDK](https://opentelemetry.io/docs/languages/go/) | 分布式追踪的 Go 集成指南 |

### TLS/mTLS

| 主题 | 资料 | 说明 |
|---|---|---|
| mTLS 原理 | [Mutual TLS Authentication](https://www.cloudflare.com/learning/access-control/what-is-mutual-tls/) | Cloudflare 的 mTLS 入门指南 |
| Go TLS 配置 | [crypto/tls](https://pkg.go.dev/crypto/tls) | tls.Config 各字段含义 |
| 证书链验证 | [x509 证书验证](https://pkg.go.dev/crypto/x509#CertPool) | RootCAs 与 VerifiedChains 的工作原理 |

### API 授权与安全

| 主题 | 资料 | 说明 |
|---|---|---|
| Scope-based vs RBAC | [OAuth 2.0 Scopes](https://oauth.net/2/scope/) | OAuth 2.0 Scope 模型的设计哲学，与本项目的 Scope-based 权限模型一脉相承 |
| 时序攻击防御 | [crypto/subtle](https://pkg.go.dev/crypto/subtle) | Go 标准库常量时间比较的密码学基础 |
| 路径归一化安全 | [OWASP Path Traversal](https://owasp.org/www-community/attacks/Path_Traversal) | 路径归一化不当导致的安全绕过（本项目的 `/api/v1/*` → `/v1/*` 归一化是正向应用） |
| 最小权限原则 | [Principle of Least Privilege](https://en.wikipedia.org/wiki/Principle_of_least_privilege) | `hub:read` vs `hub:dispatch` 分离设计的理论基础 |