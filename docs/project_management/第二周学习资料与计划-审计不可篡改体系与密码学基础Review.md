# 第二周学习资料与计划：审计日志不可篡改体系与密码学基础 Review

> **周目标**：深入理解并验证 `pkg/store` 与 `pkg/crypto` 的密码学实现，确认审计存证体系的安全基石正确无误。
>
> **审查范围**：`pkg/store/audit_hash.go`、`pkg/store/store.go`、`pkg/store/levels.go`、`pkg/store/flusher/flusher.go`、`pkg/store/sqlite/`、`pkg/store/postgres/`、`pkg/store/memory/`、`pkg/store/cmd/repairchain/`、`pkg/crypto/envelope.go`、`pkg/crypto/sm3.go`、`pkg/crypto/sm4.go`、`pkg/crypto/sm2.go`。
>
> **代码总量**：约 35 个 Go 源文件，~11,000 行实现代码 + ~3,400 行测试代码。

---

## 目录

- [第 1 章：前置知识准备](#第-1-章前置知识准备)
  - [P0：国密 SM3 杂凑算法](#p0国密-sm3-杂凑算法)
  - [P1：HMAC 密钥化消息认证码](#p1hmac-密钥化消息认证码)
  - [P2：国密 SM4 分组密码与 GCM 模式](#p2国密-sm4-分组密码与-gcm-模式)
  - [P3：HKDF 密钥派生 (RFC 5869)](#p3hkdf-密钥派生-rfc-5869)
  - [P4：哈希链与防篡改审计日志](#p4哈希链与防篡改审计日志)
  - [P5：微批异步刷盘与 FIFO 保序](#p5微批异步刷盘与-fifo-保序)
- [第 2 章：Day 1-2 不可篡改哈希链算法精读](#第-2-章day-1-2-不可篡改哈希链算法精读)
  - [2.1 审查文件清单](#21-审查文件清单)
  - [2.2 9 要素完整性前映像结构](#22-9-要素完整性前映像结构)
  - [2.3 密钥化 HMAC-SM3 存证](#23-密钥化-hmac-sm3-存证)
  - [2.4 向下兼容多轨核验](#24-向下兼容多轨核验)
  - [2.5 链核验结论枚举与规范化链序](#25-链核验结论枚举与规范化链序)
  - [2.6 手动推导示例](#26-手动推导示例)
- [第 3 章：Day 3 SM4-GCM 信封加密与密钥派生精读](#第-3-章day-3-sm4-gcm-信封加密与密钥派生精读)
  - [3.1 审查文件清单](#31-审查文件清单)
  - [3.2 SM4-GCM 工作模式](#32-sm4-gcm-工作模式)
  - [3.3 v2 信封格式详解](#33-v2-信封格式详解)
  - [3.4 HKDF-SM3 密钥派生](#34-hkdf-sm3-密钥派生)
  - [3.5 版本前缀参与 AAD](#35-版本前缀参与-aad)
  - [3.6 Fail-closed 安全策略](#36-fail-closed-安全策略)
  - [3.7 手动推导示例](#37-手动推导示例)
- [第 4 章：Day 4 微批刷盘器 BufferedAuditStore 精读](#第-4-章day-4-微批刷盘器-bufferedauditstore-精读)
  - [4.1 审查文件清单](#41-审查文件清单)
  - [4.2 单一权威机制](#42-单一权威机制)
  - [4.3 严格 FIFO 保序入队](#43-严格-fifo-保序入队)
  - [4.4 持久性优先于吞吐](#44-持久性优先于吞吐)
  - [4.5 生命周期无竞态停机](#45-生命周期无竞态停机)
  - [4.6 Flush 强一致性屏障](#46-flush-强一致性屏障)
  - [4.7 内存有界防 OOM](#47-内存有界防-oom)
  - [4.8 配置参数速查表](#48-配置参数速查表)
- [第 5 章：Day 5 存储后端实现 Review](#第-5-章day-5-存储后端实现-review)
  - [5.1 审查文件清单](#51-审查文件清单)
  - [5.2 SQLite 存储实现](#52-sqlite-存储实现)
  - [5.3 PostgreSQL 存储实现](#53-postgresql-存储实现)
  - [5.4 内存存储实现](#54-内存存储实现)
  - [5.5 分层降级逻辑](#55-分层降级逻辑)
  - [5.6 repairchain 哈希链重签工具](#56-repairchain-哈希链重签工具)
- [第 6 章：核心数据模型与接口体系深度分析](#第-6-章核心数据模型与接口体系深度分析)
- [第 7 章：audit_hash.go 不可篡改哈希链逐行代码走读](#第-7-章audit_hashgo-不可篡改哈希链逐行代码走读)
  - [7.1 进程级密钥管理 atomic.Pointer](#71-进程级密钥管理-atomicpointer)
  - [7.2 integrityPayload 前映像构建](#72-integritypayload-前映像构建)
  - [7.3 ComputeAuditIntegrityHash 完整执行路径](#73-computeauditintegrityhash-完整执行路径)
  - [7.4 VerifyAuditIntegrityHash 多轨核验策略](#74-verifyauditintegrityhash-多轨核验策略)
  - [7.5 快照独立完整性哈希设计](#75-快照独立完整性哈希设计)
  - [7.6 SM2 数字签名集成（G-10）](#76-sm2-数字签名集成g-10)
- [第 8 章：envelope.go SM4-GCM 信封加密逐行代码走读](#第-8-章envelopego-sm4-gcm-信封加密逐行代码走读)
  - [8.1 版本演进路线 v1→v2→v3](#81-版本演进路线-v1v2v3)
  - [8.2 encryptV2 完整执行路径](#82-encryptv2-完整执行路径)
  - [8.3 decryptV2/decryptV1 分派策略](#83-decryptv2decryptv1-分派策略)
  - [8.4 密钥版本注册表（G-08 密钥轮换）](#84-密钥版本注册表g-08-密钥轮换)
  - [8.5 密码操作审计日志（G-13）](#85-密码操作审计日志g-13)
- [第 9 章：flusher.go 微批刷盘器完整生命周期走读](#第-9-章flushergo-微批刷盘器完整生命周期走读)
  - [9.1 SaveLogWithSnapshot 单一权威入队全流程](#91-savelogwithsnapshot-单一权威入队全流程)
  - [9.2 flushWorker 事件循环深度析](#92-flushworker-事件循环深度析)
  - [9.3 退避重试与 backlog 保留机制](#93-退避重试与-backlog-保留机制)
  - [9.4 Flush 强一致性屏障协议](#94-flush-强一致性屏障协议)
  - [9.5 Close 无竞态停机协议](#95-close-无竞态停机协议)
  - [9.6 stageLog 读己之写暂存与有界淘汰](#96-stagelog-读己之写暂存与有界淘汰)
- [第 10 章：存储后端实现与 repairchain 工具深度 Review](#第-10-章存储后端实现与-repairchain-工具深度-review)
  - [10.1 SQLite 存储实现关键设计](#101-sqlite-存储实现关键设计)
  - [10.2 PostgreSQL 存储实现关键设计](#102-postgresql-存储实现关键设计)
  - [10.3 内存存储实现与测试一致性](#103-内存存储实现与测试一致性)
  - [10.4 repairchain 哈希链重签工具完整走读](#104-repairchain-哈希链重签工具完整走读)
- [第 11 章：代码走读指南](#第-11-章代码走读指南)
- [第 12 章：常见问题与排查指南](#第-12-章常见问题与排查指南)
- [第 13 章：术语表](#第-13-章术语表)
- [第 14 章：Review 检查清单详细版](#第-14-章review-检查清单详细版)
- [第 15 章：周交付物清单](#第-15-章周交付物清单)
- [附录 A：关键环境变量速查表](#附录-a关键环境变量速查表)
- [附录 B：密码学算法参数速查](#附录-b密码学算法参数速查)
- [附录 C：存储接口关系全景图](#附录-c存储接口关系全景图)
- [附录 D：审计存证安全设计全景图](#附录-d审计存证安全设计全景图)
- [附录 E：推荐阅读与延伸阅读](#附录-e推荐阅读与延伸阅读)

---

## 第 1 章：前置知识准备

在开始 Review 之前，需要掌握以下密码学与存储基础概念。这些知识是理解审计不可篡改体系代码的前提。

### P0：国密 SM3 杂凑算法

**SM3**（GM/T 0004-2012）是中国国家密码管理局发布的密码杂凑算法，输出 256 位（32 字节）摘要。

| 特性 | SM3 | SHA-256 |
|---|---|---|
| 输出长度 | 256 位 | 256 位 |
| 分组长度 | 512 位 | 512 位 |
| 消息填充 | 与 SHA-256 类似（1 位 + 0 填充 + 64 位长度） | 同左 |
| 压缩函数轮数 | 64 轮 | 64 轮 |
| 设计差异 | 使用独立的置换函数（消息扩展不同） | NIST 标准 |
| 法律地位 | 中国商用密码标准 | 国际标准 |

**本项目使用场景**：
- 审计日志完整性哈希计算（`pkg/store/audit_hash.go`）
- HMAC-SM3 密钥化存证（P1-2 上线口径）
- HKDF-SM3 密钥派生的底层杂凑函数（`pkg/crypto/envelope.go`）
- SM3 纯 Go 实现位于 `pkg/crypto/sm3.go`（230 行），不依赖 C 库

**关键代码片段**（`pkg/crypto/sm3.go` 核心结构）：

```go
// SM3 的初始向量（与 SHA-256 不同，使用独立的 IV）
var iv = [8]uint32{
    0x7380166f, 0x4914b2b9, 0x172442d7, 0xda8a0600,
    0xa96f30bc, 0x163138aa, 0xe38dee4d, 0xb0fb0e4e,
}

// SumSM3 计算数据的 SM3 摘要，返回 32 字节 hex 编码字符串
func SumSM3(data []byte) string {
    // 1. 消息填充（padding）：补 1 位 + 若干 0 + 64 位原始长度
    // 2. 分组迭代：每 512 位一组，经 64 轮压缩函数
    // 3. 输出：拼接 8 个 32 位寄存器，hex 编码为 64 字符
}
```

**为何选择 SM3 而非 SHA-256**：项目面向政务云与等保三级合规，国密算法是强制要求。同时保留 SHA-256 向下兼容，确保升级国密前的历史证据仍可核验。

#### SM3 完整可运行示例

```go
package main

import (
    "fmt"
    "github.com/fengzhizi319/PrivShield-go/pkg/crypto"
)

func main() {
    // 1. 基础 SM3 杂凑计算
    data := []byte("abc")
    sum := crypto.SumSM3(data)
    fmt.Printf("SM3(\"abc\") = %x\n", sum)
    // 国标测试向量：SM3("abc") = 66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0

    // 2. 空字符串的 SM3
    emptySum := crypto.SumSM3([]byte(""))
    fmt.Printf("SM3(\"\")    = %x\n", emptySum)
    // 国标测试向量：SM3("") = 1ab21d8355cfa17f8e61194831e81a8f22bec8c728fefb747ed035eb5082aa2b

    // 3. 使用 hash.Hash 接口（流式计算）
    h := crypto.NewSM3()
    h.Write([]byte("abc"))
    fmt.Printf("SM3 via hash.Hash = %x\n", h.Sum(nil))

    // 4. 大消息分块写入（模拟流式场景）
    h2 := crypto.NewSM3()
    h2.Write([]byte("ab"))
    h2.Write([]byte("c")) // 分两次写入，结果应与一次性写入相同
    fmt.Printf("SM3 chunked     = %x\n", h2.Sum(nil))
}
```

**关键点**：
- `SumSM3` 返回 `[32]byte` 固定长度数组，而非切片，避免堆分配
- `NewSM3()` 实现标准 `hash.Hash` 接口，可与 `io.MultiWriter` 等标准库组合
- 分块写入与一次性写入结果相同，因为内部维护了 64 字节缓冲区状态

---

### P1：HMAC 密钥化消息认证码

**HMAC**（Hash-based Message Authentication Code，RFC 2104）利用杂凑函数构造密钥化消息认证码。

```
HMAC(K, M) = H((K' ⊕ opad) || H((K' ⊕ ipad) || M))
```

其中：
- `K` 为密钥，`K'` 为密钥填充到分组长度
- `opad` = 0x5c 重复，`ipad` = 0x36 重复
- `H` 为底层杂凑函数（本项目取 SM3）

**本项目使用场景**：
- `HMAC-SM3(key, "SM3-HMAC:v1|" + 9要素前映像)` — 密钥化审计存证
- 使未持有密钥者无法伪造或改写审计记录
- 无密钥 SM3 仅证明"内容未被修改"（知道口径者可重算），HMAC-SM3 提供更强的防伪造保障

**关键区别**：

| 模式 | 安全性 | 适用场景 |
|---|---|---|
| 无密钥 SM3 | 知道口径者可重算伪造 | 开发/测试环境 |
| HMAC-SM3 | 未持有密钥者无法伪造 | 生产环境（必须注入密钥） |

```go
// 密钥化存证的核心计算（pkg/crypto/sm3.go）
func HMACSM3Hex(key, data []byte) string {
    sum := HMACSM3(key, data)
    return hex.EncodeToString(sum[:])
}
```

#### HMAC-SM3 完整可运行示例

```go
package main

import (
    "fmt"
    "github.com/fengzhizi319/PrivShield-go/pkg/crypto"
)

func main() {
    key := []byte("my-secret-hmac-key")

    // 1. 密钥化存证计算（模拟审计日志场景）
    payload := "prev_hash|log-001|2026-09-02T08:30:00Z|SM3-HMAC:v1|input_hash|output_hash|user|L3|{}"
    mac := crypto.HMACSM3Hex(key, []byte("SM3-HMAC:v1|"+payload))
    fmt.Printf("HMAC-SM3 = %s\n", mac) // 64 字符 hex

    // 2. 同一 payload，不同密钥 → 不同 MAC
    mac2 := crypto.HMACSM3Hex([]byte("different-key"), []byte("SM3-HMAC:v1|"+payload))
    fmt.Printf("HMAC-SM3 (diff key) = %s\n", mac2)

    // 3. 同一密钥，微小改动 → 完全不同 MAC（雪崩效应）
    payload2 := "prev_hash|log-002|2026-09-02T08:30:00Z|SM3-HMAC:v1|input_hash|output_hash|user|L3|{}"
    mac3 := crypto.HMACSM3Hex(key, []byte("SM3-HMAC:v1|"+payload2))
    fmt.Printf("HMAC-SM3 (diff payload) = %s\n", mac3)

    // 4. 验真：重新计算并比较
    expected := mac
    actual := crypto.HMACSM3Hex(key, []byte("SM3-HMAC:v1|"+payload))
    fmt.Printf("验真结果: %v\n", expected == actual)
}
```

**关键点**：
- HMAC-SM3 输出固定 64 字符 hex 编码（256 位），与 SM3 输出长度相同
- 密钥不同则 MAC 不同，即使 payload 完全相同
- payload 微小改动（如 log_id 变化）导致 MAC 完全不同（雪崩效应）
- 核验时只需重新计算并比较，无需“解密”

---

### P2：国密 SM4 分组密码与 GCM 模式

**SM4**（GB/T 32907-2016）是中国国家密码管理局发布的分组密码算法。

| 参数 | 值 |
|---|---|
| 分组长度 | 128 位（16 字节） |
| 密钥长度 | 128 位（16 字节） |
| 轮数 | 32 轮 |
| 结构 | 广义 Feistel 网络 |

**GCM（Galois/Counter Mode）** 是一种认证加密（AEAD）工作模式，同时提供机密性与完整性保护：

```
加密：C = E(K, counter) ⊕ P     （密文）
认证：T = GHASH(H, AAD, C) ⊕ E(K, J0)  （认证标签 Tag）
```

- `P` = 明文，`C` = 密文，`T` = 128-bit 认证标签
- `AAD` = 附加认证数据（本项目中为版本前缀 `enc:v2:`）
- `GHASH` = Galois 域哈希
- `counter` = 12 字节 Nonce + 4 字节计数器

**本项目使用场景**：
- 审计快照的 SM4-GCM 信封加密（`pkg/crypto/envelope.go`）
- 每次加密生成独立 salt + Nonce，确保同一明文每次加密产出不同密文
- SM4 纯 Go 实现位于 `pkg/crypto/sm4.go`（176 行）

#### SM4-GCM 完整可运行示例

```go
package main

import (
    "fmt"
    "github.com/fengzhizi319/PrivShield-go/pkg/crypto"
)

func main() {
    plaintext := "患者张三，身份证号 110101199001011234"
    secret := "my-encryption-password"

    // 1. 加密
    ciphertext, err := crypto.EncryptString(plaintext, secret)
    if err != nil {
        panic(err)
    }
    fmt.Printf("密文: %s\n", ciphertext)
    // 输出格式: enc:v2:<Base64(16B salt + 12B nonce + 密文 + 16B tag)>

    // 2. 解密
    decrypted, err := crypto.DecryptString(ciphertext, secret)
    if err != nil {
        panic(err)
    }
    fmt.Printf("解密: %s\n", decrypted)

    // 3. 同一明文每次加密产出不同密文（因 salt/nonce 随机）
    ct1, _ := crypto.EncryptString(plaintext, secret)
    ct2, _ := crypto.EncryptString(plaintext, secret)
    fmt.Printf("ct1 == ct2: %v\n", ct1 == ct2) // false

    // 4. 错误密钥解密失败（GCM 认证失败）
    _, err = crypto.DecryptString(ciphertext, "wrong-password")
    fmt.Printf("错误密钥: %v\n", err)

    // 5. 空密钥拒绝加密（fail-closed）
    _, err = crypto.EncryptString(plaintext, "")
    fmt.Printf("空密钥: %v\n", err) // ErrEmptyKey

    // 6. 无前缀密文拒绝解密（防剥离降级）
    _, err = crypto.DecryptString("plaintext-data", secret)
    fmt.Printf("无前缀: %v\n", err) // ErrUnencryptedValue

    // 7. 判断是否加密
    fmt.Printf("IsEncrypted(ct): %v\n", crypto.IsEncrypted(ciphertext))   // true
    fmt.Printf("IsEncrypted(pt): %v\n", crypto.IsEncrypted(plaintext))   // false
}
```

**关键点**：
- 同一明文每次加密产出不同密文（因 salt + nonce 随机），防止密文模式分析
- 空密钥不静默降级为明文，而是返回 `ErrEmptyKey` 拒绝加密
- 无前缀密文返回 `ErrUnencryptedValue`，防止攻击者剥离前缀降级
- 错误密钥导致 GCM 认证失败，而非返回乱码明文

---

### P3：HKDF 密钥派生 (RFC 5869)

**HKDF**（HMAC-based Key Derivation Function）分两阶段从低熵输入派生密码学强密钥：

```
Extract: PRK = HMAC-SM3(salt, IKM)     // 从输入密钥材料 + 随机 salt 提取伪随机密钥
Expand:  OKM = HMAC-SM3(PRK, info || 0x01) // 从 PRK 扩展出目标长度的密钥
```

- `IKM` = Input Keying Material（用户口令）
- `salt` = 每条记录独立的 16 字节随机值（防止相同口令在不同记录上产出相同密钥）
- `info` = 用途绑定字符串 `"PrivShield audit snapshot SM4-GCM v2"`

**为何需要逐记录 salt**：若直接用 `SHA-256(password)[:16]`（v1 旧实现），短口令的哈希截断属于弱派生，易被离线暴破。HKDF + 逐记录 salt 使每次加密产出独立密钥，即使口令相同也无法交叉攻击。

```go
// HKDF-SM3 密钥派生（pkg/crypto/envelope.go）
func DeriveKeyHKDF(secret string, salt []byte) []byte {
    prk := hkdfExtract(salt, []byte(secret))
    return hkdfExpand(prk, []byte(hkdfInfo), KeySize)
}

func hkdfExtract(salt, ikm []byte) []byte {
    mac := hmac.New(NewSM3, salt)
    mac.Write(ikm)
    return mac.Sum(nil) // 32 字节 PRK
}

func hkdfExpand(prk, info []byte, length int) []byte {
    out := make([]byte, 0, length+SM3Size)
    var prev []byte
    for i := byte(1); len(out) < length; i++ {
        mac := hmac.New(NewSM3, prk)
        mac.Write(prev)
        mac.Write(info)
        mac.Write([]byte{i})
        prev = mac.Sum(nil)
        out = append(out, prev...)
    }
    return out[:length]
}
```

#### HKDF 密钥派生完整可运行示例

```go
package main

import (
    "crypto/rand"
    "encoding/hex"
    "fmt"
)

// 模拟 HKDF 派生过程（简化版）
func main() {
    secret := "my-encryption-password"
    salt := make([]byte, 16)
    rand.Read(salt) // 每条记录独立随机 salt

    fmt.Printf("口令: %s\n", secret)
    fmt.Printf("Salt: %s\n", hex.EncodeToString(salt))

    // v1 旧派生（弱）：SHA-256(secret)[:16]
    // 同一口令永远产出相同密钥，可被离线暴破
    // v2 新派生（强）：HKDF-SM3(secret, salt)[:16]
    // 同一口令 + 不同 salt = 不同密钥

    // 模拟两次加密，使用不同 salt
    salt2 := make([]byte, 16)
    rand.Read(salt2)
    fmt.Printf("Salt2: %s\n", hex.EncodeToString(salt2))
    fmt.Printf("同一口令 + 不同 salt → 不同密钥\n")
    fmt.Printf("v1 派生: SHA-256(secret)[:16] → 固定密钥（可被离线暴破）\n")
    fmt.Printf("v2 派生: HKDF(secret, salt)[:16] → 每次独立密钥（抗暴破）\n")
}
```

**v1 vs v2 派生对比**：

| 特性 | v1 (SHA-256 截断) | v2 (HKDF-SM3 + salt) |
|---|---|---|
| 同一口令产出 | 固定相同密钥 | 每次不同密钥（因 salt 随机） |
| 抗离线暴破 | 弱（截断到 128 位） | 强（HKDF 提取全 256 位熵） |
| 抗彩虹表 | 无 salt 可被预计算 | 逐记录 salt 使预计算无效 |
| 用途绑定 | 无 | `info` 参数绑定到「审计快照加密」 |

---

### P4：哈希链与防篡改审计日志

**哈希链**（Hash Chain）是一种将每条记录的哈希值与前一条记录的哈希值绑定的链式结构：

```
Record[n].prev_hash = Record[n-1].integrity_hash
Record[n].integrity_hash = Hash(prev_hash | log_id | timestamp | ... | params_json)
```

**核心安全属性**：
- **不可篡改**：修改任意历史记录的字段会导致后续所有记录的 `prev_hash` 不匹配
- **不可插入**：攻击者无法在链中间插入伪造记录（因为不知道 HMAC 密钥）
- **不可删除**：删除记录会导致链断裂（`prev_hash` 无法衔接）

**本项目的 9 要素前映像**：
```
prev_hash|log_id|timestamp_utc|algorithm|input_hash|output_hash|user|security_level|params_json
```

为何用 `|` 分隔而非 JSON：确定性编码，消除 JSON 字段顺序歧义（`{"a":1,"b":2}` 与 `{"b":2,"a":1}` 的 JSON 序列化结果可能不同，但 `|` 分隔的拼接顺序是固定的）。

#### 多记录哈希链完整推导示例

以下演示 3 条审计日志的链式计算过程，展示哈希链的不可篡改特性：

```
记录 1（链首）：
  prev_hash     = ""（无前驱）
  log_id        = "log-001"
  timestamp     = "2026-09-02T08:00:00Z"
  input_hash    = "sm3:aaa..."
  output_hash   = "sm3:bbb..."
  user          = "service-hub"
  security_level = "L3"
  params_json   = {"op":"mask"}
  
  → integrity_hash_1 = SM3(""|log-001|2026-09-02T08:00:00Z|SM3|sm3:aaa...|sm3:bbb...|service-hub|L3|{"op":"mask"})
                     = "a1b2c3..."

记录 2：
  prev_hash     = "a1b2c3..." ← 记录 1 的 integrity_hash
  log_id        = "log-002"
  
  → integrity_hash_2 = SM3("a1b2c3..."|log-002|...)
                     = "d4e5f6..."

记录 3：
  prev_hash     = "d4e5f6..." ← 记录 2 的 integrity_hash
  log_id        = "log-003"
  
  → integrity_hash_3 = SM3("d4e5f6..."|log-003|...)
                     = "g7h8i9..."
```

**篡改检测演示**：

```
攻击者修改记录 1 的 user 字段："service-hub" → "attacker"

核验时：
  重算 integrity_hash_1' = SM3(""|log-001|...|attacker|...) = "x1y2z3..."
  但存储的 integrity_hash_1 = "a1b2c3..."
  → x1y2z3 ≠ a1b2c3 → 检测到篡改！

即使攻击者同时更新 integrity_hash_1：
  记录 2 的 prev_hash = "a1b2c3..." 但记录 1 的 integrity_hash 已变为 "x1y2z3..."
  → 记录 2 的 prev_hash 不匹配 → 链断裂！

结论：修改任意历史记录会导致后续所有记录的 prev_hash 不匹配。
```

---

### P5：微批异步刷盘与 FIFO 保序

在高并发审计写入场景下，逐条同步写入数据库会成为性能瓶颈。微批刷盘器将频繁的小事务折叠为低频的大事务：

```
调用方 ──Enqueue──> [环形缓冲队列] ──ticker──> flushWorker ──SaveLogsBatch──> 数据库
                       (10000)                  (每20ms)        (每批≤200条)
```

**关键设计约束**：
- **FIFO 保序**：哈希链要求记录严格按写入顺序落盘，乱序会导致断链
- **单一权威**：链尾哈希只能由刷盘器在服务端单点裁定，消除多路径计算的哈希分叉
- **持久性优先**：写入失败时整批保留在重试暂存区，绝不丢弃已确认记录
- **内存有界**：暂存区受 `MaxStaged` 约束，防止底层存储长期不可用时 OOM

---

## 第 2 章：Day 1-2 不可篡改哈希链算法精读

### 2.1 审查文件清单

| 文件 | 行数 | 职责 |
|---|---|---|
| `pkg/store/audit_hash.go` | 358 | 哈希链计算与核验的唯一权威实现 |
| `pkg/store/audit_hash_test.go` | 195 | 哈希链计算与核验的测试覆盖 |
| `pkg/store/store.go` | 436 | `AuditLog`、`SnapshotRecord`、`ChainVerificationResult` 数据模型与 `AuditStore` 接口定义 |
| `pkg/crypto/sm3.go` | 230 | 国密 SM3 杂凑算法的纯 Go 实现 |
| `pkg/crypto/sm3_test.go` | 76 | SM3 算法正确性测试（含国标向量） |

### 2.2 9 要素完整性前映像结构

审计日志的完整性哈希由 9 个关键字段拼接而成：

```
prev_hash|log_id|timestamp_utc|algorithm|input_hash|output_hash|user|security_level|params_json
```

**各字段含义**：

| 序号 | 字段 | 来源 | 作用 |
|---|---|---|---|
| 1 | `prev_hash` | 上一条记录的 `integrity_hash` | 链锚点，绑定前后记录 |
| 2 | `log_id` | 审计日志唯一 ID | 身份标识，防止重放 |
| 3 | `timestamp_utc` | UTC RFC3339Nano 格式 | 时间锚点，必须 UTC 归一化 |
| 4 | `algorithm` | `"SM3"` / `"SM3-HMAC:v1"` / `"SHA256"` | 算法标识，支持多轨核验 |
| 5 | `input_hash` | 脱敏前数据的 SM3 摘要 | 输入数据指纹 |
| 6 | `output_hash` | 脱敏后数据的 SM3 摘要 | 输出数据指纹 |
| 7 | `user` | 操作人身份标识 | 操作者追溯 |
| 8 | `security_level` | 数据安全等级 | 分级保护标识 |
| 9 | `params_json` | 操作参数 JSON | 操作上下文完整记录 |

**UTC 归一化的重要性**：PostgreSQL 的 `TIMESTAMPTZ` 类型在存储时会转换为 UTC，但读取时可能附加服务端本地时区偏移。如果前映像中的时间戳不做 UTC 归一化，写入方和核验方可能因时区偏移不同而算出不同的哈希值，产生"伪分叉"（看似断链，实为时区不一致）。

```go
// integrityPayload 构建前映像（pkg/store/audit_hash.go）
func integrityPayload(log *AuditLog, inUTC bool) string {
    ts := log.Timestamp
    if inUTC {
        ts = ts.UTC()
    }
    return strings.Join([]string{
        log.PrevHash,
        log.ID,
        ts.Format(time.RFC3339Nano),  // UTC 纳秒级归一化
        log.Algorithm,
        log.InputHash,
        log.OutputHash,
        log.User,
        log.SecurityLevel,
        log.ParamsJSON,
    }, "|")
}
```

### 2.3 密钥化 HMAC-SM3 存证

当配置了 `AUDIT_LOG_HASH_KEY` 环境变量后，新写入的审计记录采用密钥化存证：

```
integrity_hash = HMAC-SM3(key, "SM3-HMAC:v1|" + 9要素前映像)
algorithm = "SM3-HMAC:v1"
```

**安全语义**：
- 无密钥 SM3：知道口径者可以重算并伪造，仅证明"内容未被意外修改"
- 密钥化 HMAC-SM3：未持有密钥者无法伪造或改写记录，提供真正的防篡改保障

```go
// ComputeAuditIntegrityHash 计算审计日志完整性哈希（pkg/store/audit_hash.go）
func ComputeAuditIntegrityHash(log *AuditLog) string {
    payload := integrityPayload(log, true) // UTC 归一化
    key := AuditChainKey()

    if key != "" {
        // 密钥化 HMAC-SM3（P1-2 生产口径）
        mac := hmac.New(crypto.NewSM3, []byte(key))
        mac.Write([]byte("SM3-HMAC:v1|" + payload))
        return hex.EncodeToString(mac.Sum(nil))
    }
    // 无密钥 SM3（开发/测试降级）
    return crypto.SumSM3([]byte(payload))
}
```

**进程级密钥管理**：`chainKey` 使用 `atomic.Pointer[string]` 存储，进程启动时注入一次，运行期不可更改（改钥会导致既有记录核验失败）。

### 2.4 向下兼容多轨核验

`VerifyAuditIntegrityHash` 需要能够核验不同时期写入的记录。由于系统经历了 SHA-256 -> SM3 -> HMAC-SM3 的升级路径，核验函数依次尝试 5 种候选：

```
候选 1：HMAC-SM3(key, "SM3-HMAC:v1|" + UTC前映像)     ← 当前生产口径
候选 2：SM3(UTC前映像)                                  ← 国密升级后、密钥化前
候选 3：SHA-256(UTC前映像)                              ← 国密升级前
候选 4：SM3(LocalTZ前映像)                              ← 早期 bug（未 UTC 归一化）
候选 5：SHA-256(LocalTZ前映像)                          ← 最早期版本
```

```go
// VerifyAuditIntegrityHash 多轨核验（pkg/store/audit_hash.go）
func VerifyAuditIntegrityHash(log *AuditLog, expected string) (bool, string) {
    key := AuditChainKey()
    candidates := buildCandidates(log, key) // 构建 5 种候选

    for _, c := range candidates {
        if hmac.Equal([]byte(c.digest(c.payload)), []byte(expected)) {
            return true, c.label
        }
    }
    return false, ""
}
```

**为何需要兼容**：加密产品认证前写入的历史证据（使用 SHA-256 或无 UTC 归一化）仍需合法可验，不能因为算法升级导致旧证据全部"断链"。

### 2.5 链核验结论枚举与规范化链序

**链核验结论**（`ChainReason*` 常量）：

| 结论常量 | 含义 | 触发条件 |
|---|---|---|
| `ChainReasonOK` | 链完整且一致 | 所有记录的哈希均匹配 |
| `ChainReasonLegacyHashed` | 链完整但使用旧算法 | 记录使用 SHA-256 或 LocalTZ |
| `ChainReasonTamperedPayload` | 数据被篡改 | 字段值与存储的哈希不匹配 |
| `ChainReasonBrokenChain` | 链断裂 | `prev_hash` 与上一条的 `integrity_hash` 不匹配 |
| `ChainReasonMissingPrev` | 前驱记录缺失 | 链中缺少某条记录 |
| `ChainReasonMissingRecords` | 序号不连续 | `seq` 字段存在跳跃 |

**规范化链序**：`(seq ASC, timestamp ASC, id ASC)` 三元组排序。保证同时间戳记录在任何后端与工具上都以同一顺序回放，不产生伪分叉。

### 2.6 手动推导示例

以下演示一条审计日志的 HMAC-SM3 密钥化存证计算过程：

**输入**：
```
prev_hash    = "a1b2c3..."（上一条的 integrity_hash）
log_id       = "log-20260902-001"
timestamp    = 2026-09-02T08:30:00.123456789Z (UTC)
algorithm    = "SM3-HMAC:v1"
input_hash   = "sm3:abc123..."
output_hash  = "sm3:def456..."
user         = "service-hub"
security_level = "L3"
params_json  = {"operation":"mask","datasource":"ds_yibao"}
key          = "my-secret-hmac-key"
```

**步骤 1**：拼接 9 要素前映像
```
preimage = "a1b2c3...|log-20260902-001|2026-09-02T08:30:00.123456789Z|SM3-HMAC:v1|sm3:abc123...|sm3:def456...|service-hub|L3|{\"operation\":\"mask\",\"datasource\":\"ds_yibao\"}"
```

**步骤 2**：计算 HMAC-SM3
```
integrity_hash = HMAC-SM3("my-secret-hmac-key", "SM3-HMAC:v1|" + preimage)
               = "e5f6a7b8c9d0..."（64 字符 hex 编码）
```

**步骤 3**：写入数据库
```sql
INSERT INTO audit_logs (..., algorithm, integrity_hash)
VALUES (..., 'SM3-HMAC:v1', 'e5f6a7b8c9d0...');
```

---

## 第 3 章：Day 3 SM4-GCM 信封加密与密钥派生精读

### 3.1 审查文件清单

| 文件 | 行数 | 职责 |
|---|---|---|
| `pkg/crypto/envelope.go` | 521 | SM4-GCM 信封加密/解密实现 |
| `pkg/crypto/envelope_test.go` | 211 | 加解密往返测试与边界条件 |
| `pkg/crypto/sm4.go` | 176 | 国密 SM4 分组密码算法的纯 Go 实现 |

### 3.2 SM4-GCM 工作模式

SM4-GCM 结合了 SM4 分组密码与 GCM 认证加密模式：

```
明文 P ──┐
         ├── SM4-GCM-Encrypt(K, Nonce, AAD) ──> 密文 C + 认证标签 T
AAD ─────┘

密文 C + 认证标签 T ──┐
                      ├── SM4-GCM-Decrypt(K, Nonce, AAD) ──> 明文 P
AAD ──────────────────┘
```

- **机密性**：SM4 加密保护明文不泄露
- **完整性**：GCM 认证标签保护密文不被篡改
- **AAD 绑定**：附加认证数据参与认证但不加密（本项目中为版本前缀 `enc:v2:`）

### 3.3 v2 信封格式详解

当前写入的密文格式（v2）：

```
enc:v2:<Base64( 16字节 salt + 12字节 Nonce + SM4 密文 + 16字节 Tag )>
```

各字段作用：

| 字段 | 长度 | 作用 |
|---|---|---|
| `enc:v2:` | 7 字节 | 版本前缀，参与 AAD 认证 |
| `salt` | 16 字节 | HKDF 密钥派生的逐记录随机盐 |
| `Nonce` | 12 字节 | GCM 标准 96-bit 随机数 |
| `SM4 密文` | 与明文等长 | SM4-GCM 加密输出 |
| `Tag` | 16 字节 | 128-bit GCM 认证标签 |

**为何每个字段都不可或缺**：
- 缺少 `salt`：同一口令在不同记录上产出相同密钥，可被离线暴破
- 缺少 `Nonce`：GCM 要求每次加密使用唯一 Nonce，复用 Nonce 会完全破坏安全性
- 缺少 `Tag`：无法检测密文是否被篡改
- 前缀不参与 AAD：攻击者可剥离前缀降级为明文而不被检测

### 3.4 HKDF-SM3 密钥派生

v2 格式使用 HKDF-SM3 从口令派生 SM4 密钥：

```
                    ┌──────────────┐
 口令 (IKM) ──────>│              │
                    │   Extract    │──> PRK (32 字节伪随机密钥)
 salt (16B) ──────>│  (HMAC-SM3)  │
                    └──────┬───────┘
                           │
                    ┌──────▼───────┐
                    │   Expand     │──> SM4 Key (16 字节)
 info ────────────>│  (HMAC-SM3)  │
                    └──────────────┘
```

```go
// DeriveKeyHKDF 从口令与逐记录随机 salt 派生 16 字节 SM4 密钥
func DeriveKeyHKDF(secret string, salt []byte) ([]byte, error) {
    // Extract: PRK = HMAC-SM3(salt, IKM)
    prk := hmac.New(crypto.NewSM3, salt)
    prk.Write([]byte(secret))
    extracted := prk.Sum(nil) // 32 字节

    // Expand: OKM = HMAC-SM3(PRK, info || 0x01)[:16]
    expander := hmac.New(crypto.NewSM3, extracted)
    expander.Write([]byte(hkdfInfo)) // "PrivShield audit snapshot SM4-GCM v2"
    expander.Write([]byte{0x01})
    return expander.Sum(nil)[:16], nil // SM4 需要 16 字节密钥
}
```

### 3.5 版本前缀参与 AAD

这是本项目一个精妙的设计：版本前缀 `enc:v2:` 作为 GCM 的 AAD 参与认证计算。

```go
// Seal 加密：前缀参与 AAD
func (e *EnvelopeEncryptor) Seal(plaintext string) (string, error) {
    // ... 生成 salt、nonce、派生密钥 ...
    aad := []byte(EncryptedPrefixV2) // "enc:v2:" 作为 AAD
    ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), aad)
    return EncryptedPrefixV2 + base64.StdEncoding.EncodeToString(
        append(salt, append(nonce, ciphertext...)...),
    ), nil
}
```

**安全意义**：如果攻击者将 `enc:v2:` 替换为 `enc:v1:`（试图降级为弱派生），GCM 认证会失败，因为 AAD 不匹配。这消除了"去前缀即降级"的静默通道。

### 3.6 Fail-closed 安全策略

v2 实现采用 fail-closed 设计，所有异常路径都拒绝而非降级：

| 异常场景 | 行为 | 错误 |
|---|---|---|
| 密钥为空 | 拒绝加密，返回错误 | `ErrEmptyKey` |
| 密文无前缀 | 拒绝解密，返回错误 | `ErrUnencryptedValue` |
| 密文前缀不识别 | 拒绝解密，返回错误 | `ErrUnencryptedValue` |
| GCM 认证失败 | 拒绝解密，返回错误 | GCM `Open` 错误 |

```go
// EncryptString 加密字符串（fail-closed）
func (e *EnvelopeEncryptor) EncryptString(plaintext string) (string, error) {
    if e.keyProvider == nil {
        return "", ErrEmptyKey // 空密钥不静默降级为明文
    }
    // ...
}

// DecryptString 解密字符串
func (e *EnvelopeEncryptor) DecryptString(ciphertext string) (string, error) {
    if !strings.HasPrefix(ciphertext, EncryptedPrefixV2) &&
       !strings.HasPrefix(ciphertext, EncryptedPrefix) {
        return "", ErrUnencryptedValue // 无前缀视为被篡改
    }
    // ...
}
```

### 3.7 手动推导示例

以下演示一条明文使用 v2 信封加密的完整过程：

**输入**：
```
plaintext = "患者张三，身份证号 110101199001011234"
secret    = "my-encryption-password"
```

**步骤 1**：生成随机数
```
salt  = rand(16) = "a1b2c3d4e5f6..."（每记录独立随机）
nonce = rand(12) = "f1e2d3c4b5a6..."（GCM 96-bit Nonce）
```

**步骤 2**：HKDF-SM3 派生密钥
```
PRK  = HMAC-SM3(salt, secret)           = "0x1a2b3c..."（32 字节）
key  = HMAC-SM3(PRK, info || 0x01)[:16] = "0x4d5e6f..."（16 字节 SM4 密钥）
```

**步骤 3**：SM4-GCM 加密
```
aad        = "enc:v2:"
ciphertext = SM4-GCM-Seal(key, nonce, plaintext, aad)  // 密文 + 16字节 Tag
```

**步骤 4**：组装输出
```
payload = salt + nonce + ciphertext + tag
result  = "enc:v2:" + Base64(payload)
```

---

## 第 4 章：Day 4 微批刷盘器 BufferedAuditStore 精读

### 4.1 审查文件清单

| 文件 | 行数 | 职责 |
|---|---|---|
| `pkg/store/flusher/flusher.go` | 816 | 内存缓冲微批异步刷盘器核心实现 |
| `pkg/store/flusher/flusher_test.go` | 593 | 正常刷盘、存储故障重试、优雅停机、积压饱和测试 |

### 4.2 单一权威机制

**问题**：在高并发场景下，多个 goroutine 可能同时计算审计日志的 `prev_hash` 和 `integrity_hash`。如果各自独立计算，会导致：
- 日志行（数据库写入）的哈希
- 快照行（加密快照）的哈希
- HTTP/gRPC 响应体的哈希

三者可能不一致（因为并发写入的顺序不确定），形成"哈希分叉"。

**解决方案**：`prev_hash` 与 `integrity_hash` 只能由刷盘器在服务端单点裁定。入队时即在锁内确定哈希值并同步写回调用方指针。

```go
// SaveLog 入队时由刷盘器裁定哈希（pkg/store/flusher/flusher.go）
func (b *BufferedAuditStore) SaveLog(log *store.AuditLog) error {
    b.stateMu.Lock()
    // 在临界区内：
    // 1. 计算 prev_hash = b.lastHash（链尾）
    // 2. 计算 integrity_hash = Hash(prev_hash | ... | params)
    // 3. 更新 b.lastHash = integrity_hash
    // 4. 将计算结果写回 log 指针
    log.PrevHash = b.lastHash
    log.IntegrityHash = computeHash(log)
    b.lastHash = log.IntegrityHash
    b.stateMu.Unlock()

    // 入队到缓冲队列（此时哈希已确定）
    return b.enqueue(log)
}
```

### 4.3 严格 FIFO 保序入队

哈希链要求记录严格按写入顺序落盘。如果记录 B 先于记录 A 落盘，链会断裂：

```
正确顺序：A.prev_hash = X, B.prev_hash = hash(A)  ← 链连续
错误顺序：B 先落盘（prev_hash = X），A 后落盘（prev_hash = hash(B)） ← 链断裂
```

**实现**：链推进与入队成功在临界区内原子完成。队列拥塞时按 `EnqueueTimeout`（默认 500ms）有界等待。

```go
// enqueue 保序入队（pkg/store/flusher/flusher.go）
func (b *BufferedAuditStore) enqueue(item *flushItem) error {
    timer := time.NewTimer(b.config.EnqueueTimeout)
    defer timer.Stop()

    select {
    case b.queue <- item:
        return nil // 成功入队
    case <-timer.C:
        return fmt.Errorf("enqueue timeout after %v", b.config.EnqueueTimeout)
    case <-b.stopCh:
        return ErrStoreClosed
    }
}
```

### 4.4 持久性优先于吞吐

底层写入失败时，整批记录保留在工作线程暂存区（retry backlog），下一轮按原序优先重投：

```
正常路径：  queue ──ticker──> flushWorker ──SaveLogsBatch──> DB ✓
失败路径：  queue ──ticker──> flushWorker ──SaveLogsBatch──> DB ✗
                                                  │
                                                  ▼
                                          retryBacklog（按原序保留）
                                                  │
                                                  ▼ （下一轮优先重投）
                                              DB ✓
```

**退避重试策略**：`25ms * 2^attempt`（第 1 次 25ms，第 2 次 50ms，第 3 次 100ms）。

```go
// flushWorker 工作线程事件循环（pkg/store/flusher/flusher.go）
func (b *BufferedAuditStore) flushWorker() {
    ticker := time.NewTicker(b.config.FlushInterval)
    for {
        select {
        case <-ticker.C:
            batch := b.drainBatch()
            if err := b.inner.SaveLogsBatch(batch); err != nil {
                // 写入失败：整批保留在 retryBacklog，下轮优先重投
                b.retryBacklog = append(b.retryBacklog, batch...)
                b.health.Store(healthDegraded)
            }
        case <-b.stopCh:
            return
        }
    }
}
```

### 4.5 生命周期无竞态停机

停机流程保证不丢失数据、不写入已关闭的句柄：

```
Close() 调用
    │
    ├── 1. stateMu.Lock() → closed = true → stateMu.Unlock()
    │      （后续 Enqueue 立即返回 ErrStoreClosed）
    │
    ├── 2. close(stopCh)
    │      （通知 flushWorker 停止 ticker）
    │
    ├── 3. 等待 flushWorker 排空（最多 CloseTimeout = 10s）
    │      （排空 = 队列 + retryBacklog 全部落盘）
    │
    └── 4. 排空超时？
           ├── 已清空 → 关闭底层存储
           └── 未清空 → 如实报告搁浅条数，不关闭底层存储
                        （避免被抛弃的工作线程写入已关闭句柄）
```

### 4.6 Flush 强一致性屏障

`Flush` 返回 nil 当且仅当队列与工作线程暂存区均已清空并成功提交。这确保 `ListLogs`/`GetStats`/`VerifyChain` 等读路径不会在数据尚未落盘时给出虚假结论。

```go
// Flush 强一致性屏障（pkg/store/flusher/flusher.go）
func (b *BufferedAuditStore) Flush() error {
    // 1. 发送 flush 请求到工作线程
    done := make(chan error, 1)
    b.flushReq <- done

    // 2. 等待工作线程确认（最多 FlushTimeout = 5s）
    select {
    case err := <-done:
        return err
    case <-time.After(b.config.FlushTimeout):
        return fmt.Errorf("flush timeout after %v", b.config.FlushTimeout)
    }
}
```

### 4.7 内存有界防 OOM

两个关键内存结构都有上限：

| 结构 | 上限 | 淘汰策略 | 作用 |
|---|---|---|---|
| `recentLogs`（读己之写暂存） | `MaxStaged` = 50000 | 入队序淘汰最旧 | 支持 SaveLog 后立即 Get 的一致性 |
| `retryBacklog`（重试暂存区） | `MaxStaged` = 50000 | 超限快速拒绝 | 防止底层存储长期不可用时 OOM |

当重试暂存区达到上限时，新写入请求收到 `ErrBacklogSaturated` 快速拒绝，而非无限积压导致 OOM。

### 4.8 配置参数速查表

| 参数 | 默认值 | 含义 | 调优建议 |
|---|---|---|---|
| `BufferSize` | 10000 | 环形缓冲队列容量 | 高并发场景可调至 50000 |
| `MaxBatchSize` | 200 | 单批最大写入条数 | SQLite 建议 ≤ 200（单写者锁） |
| `FlushInterval` | 20ms | 最长刷盘等待窗口 | 低延迟场景可调至 5ms |
| `EnqueueTimeout` | 500ms | 队列满时等待超时 | 过短会丢数据，过长会阻塞调用方 |
| `FlushTimeout` | 5s | Flush 屏障等待超时 | 通常无需调整 |
| `CloseTimeout` | 10s | 优雅停机排空超时 | 根据数据量调整 |
| `MaxRetries` | 3 | 单批重试次数 | 过多会延长降级时间 |
| `MaxStaged` | 50000 | 暂存区上限 | 根据可用内存调整 |

---

## 第 5 章：Day 5 存储后端实现 Review

### 5.1 审查文件清单

| 文件 | 行数 | 职责 |
|---|---|---|
| `pkg/store/sqlite/audit.go` | 896 | SQLite 审计存储实现 |
| `pkg/store/sqlite/init.go` | 502 | SQLite 初始化与 Schema 管理 |
| `pkg/store/sqlite/sqlite_test.go` | 958 | SQLite 全量测试 |
| `pkg/store/postgres/audit.go` | 1074 | PostgreSQL 审计存储实现 |
| `pkg/store/postgres/schema.go` | 83 | PostgreSQL Schema 定义 |
| `pkg/store/memory/memory.go` | 914 | 内存审计存储实现（测试用） |
| `pkg/store/levels.go` | 49 | 分层存储降级逻辑 |
| `pkg/store/cmd/repairchain/main.go` | 465 | 哈希链重签工具 |

### 5.2 SQLite 存储实现

SQLite 是默认的生产存储后端，关键设计点：

- **WAL 模式**：`PRAGMA journal_mode=WAL`，允许并发读写（单写者但多读者）
- **`BEGIN IMMEDIATE`**：写入事务使用 `BEGIN IMMEDIATE` 而非 `BEGIN DEFERRED`，防止写锁升级死锁
- **连接池**：`sql.DB.SetMaxOpenConns(1)` — SQLite 只允许一个写连接
- **哈希链计算**：在应用层（`BufferedAuditStore`）完成，SQLite 只负责持久化

```go
// SQLite 写入事务（pkg/store/sqlite/audit.go）
func (s *SQLiteAuditStore) SaveLogsBatch(logs []*store.AuditLog) error {
    tx, err := s.db.BeginTx(ctx, nil) // BEGIN IMMEDIATE
    // ... 批量 INSERT ...
    return tx.Commit()
}
```

### 5.3 PostgreSQL 存储实现

PostgreSQL 用于高可用生产部署，关键设计点：

- **写入只角色自检**：连接时使用 `writeonly` 角色，只能 INSERT/UPDATE，不能 SELECT（防止审计数据泄露）
- **连接池**：`SetMaxOpenConns(25)`、`SetMaxIdleConns(10)`、`SetConnMaxLifetime(5min)`
- **租约抢占**：`FOR UPDATE SKIP LOCKED` 实现多副本无阻塞任务抢占
- **分区表**：审计日志按月分区（`audit_logs_2026_09`），提升查询性能与清理效率

### 5.4 内存存储实现

内存存储用于单元测试与开发调试：

- 使用 `sync.RWMutex` 保护内存数据结构
- 所有操作在内存中完成，无持久化
- 进程重启后数据丢失
- 实现了完整的 `AuditStore` 接口，确保测试与生产行为一致

### 5.5 分层降级逻辑

当主存储后端不可用时，系统按以下顺序降级：

```
PostgreSQL ──故障──> SQLite ──故障──> 内存（最终兜底）
```

```go
// 分层降级（pkg/store/levels.go）
func NewAuditStore(config Config) (AuditStore, error) {
    if config.PostgresDSN != "" {
        store, err := postgres.NewAuditStore(config.PostgresDSN)
        if err == nil {
            return store, nil
        }
        slog.Warn("PostgreSQL unavailable, falling back to SQLite", "error", err)
    }
    if config.SQLitePath != "" {
        store, err := sqlite.NewAuditStore(config.SQLitePath)
        if err == nil {
            return store, nil
        }
        slog.Warn("SQLite unavailable, falling back to memory", "error", err)
    }
    return memory.NewAuditStore(), nil
}
```

### 5.6 repairchain 哈希链重签工具

`repairchain` 工具用于修复历史记录的哈希链（当算法升级或口径变更后）：

**工作流程**：
1. 读取存量审计记录（按规范化链序排序）
2. 识别非规范哈希标签（带 `-LEGACY` 后缀或算法不匹配）
3. 用当前写入口径重新计算 `integrity_hash`
4. 批量更新数据库中的哈希值

```bash
# 使用 repairchain 工具
go run ./pkg/store/cmd/repairchain \
  -sqlite-path /data/audit.db \
  -dry-run  # 先预览，不加此参数则实际执行
```

---

## 第 6 章：核心数据模型与接口体系深度分析

`pkg/store/store.go` 定义了三大存储接口：

```
┌─────────────────────────────────────────────────────────────┐
│                    pkg/store/store.go                        │
├─────────────────────────────────────────────────────────────┤
│  TaskStore ──────── 任务 CRUD、状态过滤、统计聚合           │
│       │                                                     │
│       └── LeasedTaskStore ── 多副本租约抢占、续期、回收     │
│                                                     │       │
│  DataSourceStore ─── 数据源注册、配置持久化            │       │
│                                                     │       │
│  AuditStore ─────── 审计日志写入、链核验、快照加密    │       │
│       │                                             │       │
│       ├── SQLiteAuditStore                          │       │
│       ├── PostgresAuditStore                        │       │
│       ├── MemoryAuditStore                          │       │
│       └── BufferedAuditStore（装饰器模式包装上述三者）│       │
└─────────────────────────────────────────────────────────────┘
```

**AuditStore 接口核心方法**：

| 方法 | 职责 | 调用方 |
|---|---|---|
| `SaveLog` | 写入单条审计日志 | BufferedAuditStore 内部 |
| `SaveLogsBatch` | 批量写入审计日志 | BufferedAuditStore flushWorker |
| `ListLogs` | 分页查询审计日志 | audit-log REST API |
| `GetStats` | 统计聚合 | audit-log 监控端点 |
| `VerifyChain` | 全量哈希链核验 | audit-log 对账任务 |
| `SaveSnapshot` | 保存加密脱敏快照 | service-hub 任务完成时 |

---

## 第 7 章：audit_hash.go 不可篡改哈希链逐行代码走读

`pkg/store/audit_hash.go`（359 行）是整个审计不可篡改体系的核心计算引擎。它定义了哈希链的计算与核验的**唯一权威实现**，所有存储后端（SQLite / PostgreSQL / 内存）都必须调用本包的函数来计算完整性哈希。

### 7.1 进程级密钥管理 atomic.Pointer

```go
// chainKey 是进程级存证哈希 HMAC 密钥（启动时由 SetAuditChainKey 注入一次）
var chainKey atomic.Pointer[string]

// SetAuditChainKey 注入密钥化存证哈希的 HMAC-SM3 密钥
func SetAuditChainKey(key string) {
    trimmed := strings.TrimSpace(key)
    chainKey.Store(&trimmed)  // 原子存储指针
}

// AuditChainKey 返回当前生效的存证哈希 HMAC 密钥
func AuditChainKey() string {
    if p := chainKey.Load(); p != nil {
        return *p  // 解引用原子指针
    }
    return ""
}
```

**设计要点**：
- 使用 `atomic.Pointer[string]` 而非 `sync.Mutex` 保护密钥：读操作完全无锁，零竞争开销
- 进程启动时注入一次，运行期不可更改（改钥会导致既有记录核验失败）
- `TrimSpace` 防止环境变量末尾的空白字符被误纳入密钥
- 空串表示「无密钥态」，退回无密钥 SM3 口径

**为何不用 `sync.Once` + 不可变变量？**
因为测试需要 `ResetAuditChainKey()` 重置密钥状态，`atomic.Pointer` 支持多次写入（虽然生产环境只写一次）。

### 7.2 integrityPayload 前映像构建

```go
func integrityPayload(logID, prevHash string, timestamp time.Time, 
    algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string, 
    inUTC bool) string {
    ts := timestamp.Format(time.RFC3339Nano)
    if inUTC {
        ts = timestamp.UTC().Format(time.RFC3339Nano)  // UTC 归一化
    }
    return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%v",
        prevHash, logID, ts, algorithm, inputHash, outputHash, user, securityLevel, paramsJSON)
}
```

**9 要素拼接顺序的设计原则**：

| 序号 | 字段 | 为何在此位置 | 篡改影响 |
|---|---|---|---|
| 1 | `prev_hash` | 链锚点，必须首位 | 断链 |
| 2 | `log_id` | 身份标识，防重放 | 哈希不匹配 |
| 3 | `timestamp_utc` | 时间锚点，必须 UTC | 哈希不匹配（时区归一化） |
| 4 | `algorithm` | 算法标识，支持多轨 | 哈希不匹配 |
| 5 | `input_hash` | 输入数据指纹 | 哈希不匹配 |
| 6 | `output_hash` | 输出数据指纹 | 哈希不匹配 |
| 7 | `user` | 操作者追溯 | 哈希不匹配 |
| 8 | `security_level` | 分级保护标识 | 哈希不匹配 |
| 9 | `params_json` | 操作参数完整记录 | 哈希不匹配 |

**UTC 归一化的关键性**：PostgreSQL 的 `TIMESTAMPTZ` 类型存储时转换为 UTC，但读取时可能附加服务端本地时区偏移。如果前映像不做 UTC 归一化，写入方和核验方可能因时区偏移不同而算出不同的哈希值，产生「伪分叉」。

### 7.3 ComputeAuditIntegrityHash 完整执行路径

```go
func ComputeAuditIntegrityHash(logID, prevHash string, timestamp time.Time,
    algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) string {
    // 步骤 1：构建 UTC 归一化的 9 要素前映像
    payload := integrityPayload(logID, prevHash, timestamp, algorithm,
        inputHash, outputHash, user, securityLevel, paramsJSON, true)
    
    // 步骤 2：检查是否配置了 HMAC 密钥
    if key := AuditChainKey(); key != "" {
        // 密钥化 HMAC-SM3（P1-2 生产口径）
        return crypto.HMACSM3Hex([]byte(key), []byte(AuditHashSM3HMAC+"|"+payload))
    }
    // 步骤 3：无密钥 SM3（开发/测试降级）
    sum := crypto.SumSM3([]byte(payload))
    return hex.EncodeToString(sum[:])
}
```

**执行流程图**：

```
ComputeAuditIntegrityHash()
    │
    ├── integrityPayload(inUTC=true)
    │   └── "prev_hash|log_id|timestamp_utc|algorithm|input_hash|output_hash|user|level|params"
    │
    ├── AuditChainKey() != "" ?
    │   ├── YES → HMACSM3Hex(key, "SM3-HMAC:v1|" + payload)
    │   │         └── 64 字符 hex 编码
    │   │
    │   └── NO  → SumSM3(payload)
    │             └── 64 字符 hex 编码
    │
    └── 返回 integrity_hash 字符串
```

### 7.4 VerifyAuditIntegrityHash 多轨核验策略

核验函数是本模块最复杂的部分。它需要能够核验不同时期写入的记录，因为系统经历了 SHA-256 → SM3 → HMAC-SM3 的升级路径。

```go
func VerifyAuditIntegrityHash(stored, logID, prevHash string, timestamp time.Time,
    algorithm, inputHash, outputHash, user, securityLevel, paramsJSON string) (bool, string) {
    if stored == "" {
        return false, ""
    }
    // 构建 UTC 归一化前映像
    utc := integrityPayload(logID, prevHash, timestamp, algorithm,
        inputHash, outputHash, user, securityLevel, paramsJSON, true)

    candidates := make([]hashCandidate, 0, 5)
    
    // 候选 1：HMAC-SM3（配置密钥时）
    if key := AuditChainKey(); key != "" {
        candidates = append(candidates, hashCandidate{
            label: AuditHashSM3HMAC,
            digest: func(payload string) string {
                return crypto.HMACSM3Hex([]byte(key), []byte(AuditHashSM3HMAC+"|"+payload))
            },
            payload: utc,
        })
    }
    
    // 候选 2：无密钥 SM3-UTC
    // 候选 3：SHA-256-UTC（历史向下兼容）
    candidates = append(candidates, ...)
    
    // 候选 4-5：LocalTZ 变体（早期 bug 兼容）
    if local := integrityPayload(..., false); local != utc {
        candidates = append(candidates, ...)
    }

    // 常量时间比较，防止时序侧信道
    storedBytes := []byte(stored)
    for _, c := range candidates {
        if hmac.Equal(storedBytes, []byte(c.digest(c.payload))) {
            return true, c.label
        }
    }
    return false, ""
}
```

**5 种候选的完整列表**：

| 候选 | 算法 | 时区 | 适用场景 |
|---|---|---|---|
| 1 | HMAC-SM3 | UTC | 当前生产口径（密钥化后） |
| 2 | SM3 | UTC | 国密升级后、密钥化前 |
| 3 | SHA-256 | UTC | 国密升级前 |
| 4 | SM3 | LocalTZ | 早期 bug（未 UTC 归一化） |
| 5 | SHA-256 | LocalTZ | 最早期版本 |

**为何使用 `hmac.Equal` 而非 `==`？**
`hmac.Equal` 执行常量时间比较，无论匹配在第几个字节失败，耗时都相同。如果用 `==`，攻击者可以通过测量响应时间差异逐字节爆破正确的哈希值（时序侧信道攻击）。

### 7.5 快照独立完整性哈希设计

快照（SnapshotRecord）在旧实现中直接复制主日志的 `integrity_hash`，这意味着如果攻击者替换快照的加密样本而不改变主日志，不会被检测。新实现为快照计算独立的完整性哈希：

```go
// 快照前映像（8 要素，与日志的 9 要素不同）
func snapshotIntegrityPayload(snapshotID, auditLogID, prevHash string,
    timestamp time.Time, algorithm, inputSample, outputSample, parametersJSON string,
    inUTC bool) string {
    // ...
    return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s",
        prevHash, snapshotID, auditLogID, ts, algorithm, inputSample, outputSample, parametersJSON)
}
```

**关键区别**：
- 快照前映像包含 `inputSample` 和 `outputSample`（加密后的样本数据）
- 替换样本会导致快照哈希变化，即使主日志未变
- 快照的 `prev_hash` 指向主日志的 `integrity_hash`，形成「主链 + 子链」双链结构

### 7.6 SM2 数字签名集成（G-10）

三级等保/密评要求审计存证具备不可否认性，仅靠哈希链不够（持有 HMAC 密钥者可伪造）。SM2 数字签名提供第二层保障：

```go
// SM2Signer 是 SM2 私钥签名器接口
type SM2Signer interface {
    Sign(data []byte) ([]byte, error)
}

// SM2Verifier 是 SM2 公钥验签器接口
type SM2Verifier interface {
    Verify(data, signature []byte) bool
}

// SignAuditRecord 使用 SM2 私钥对审计记录签名
func SignAuditRecord(integrityHash string) string {
    if sm2Signer == nil || integrityHash == "" {
        return ""  // 未配置签名器时返回空串（向下兼容）
    }
    sig, err := sm2Signer.Sign([]byte(integrityHash))
    if err != nil {
        return ""
    }
    return hex.EncodeToString(sig)
}
```

**设计要点**：
- 签名对象是 `integrity_hash`（而非原始数据），签名长度固定且短
- 接口化设计：签名器和验签器通过全局变量注册，生产环境注入真实 SM2 实现，测试环境可不注入
- 未配置 SM2 时不影响哈希链核验（向下兼容）

---

## 第 8 章：envelope.go SM4-GCM 信封加密逐行代码走读

`pkg/crypto/envelope.go`（522 行）实现了审计快照的信封加密与解密，经历了 v1 → v2 → v3 三代格式演进。

### 8.1 版本演进路线 v1→v2→v3

```
v1（历史存量，仅可读）：
  enc:v1:<Base64( 12B Nonce + SM4 密文 + 16B Tag )>
  密钥 = SHA-256(secret)[:16]  ← 弱派生，无 salt
  AAD = nil                    ← 前缀不参与认证

v2（当前写入格式）：
  enc:v2:<Base64( 16B salt + 12B Nonce + SM4 密文 + 16B Tag )>
  密钥 = HKDF-SM3(secret, salt)[:16]  ← 强派生，逐记录 salt
  AAD = "enc:v2:"                     ← 前缀参与认证

v3（密钥轮换格式，G-08）：
  enc:v3:<version>:<Base64( 16B salt + 12B Nonce + SM4 密文 + 16B Tag )>
  密钥 = HKDF-SM3(keyRegistry[version], salt)[:16]
  AAD = "enc:v3:<version>:"           ← 版本绑定认证
```

**为何需要 v3？**
- v2 使用单一口令派生密钥，无法实现密钥轮换
- v3 引入密钥版本注册表，支持「活跃密钥加密写入 + 历史密钥解密读取」
- AAD 绑定到具体版本，防止攻击者将 v3 密文降级为 v2

### 8.2 encryptV2 完整执行路径

```go
func encryptV2(plaintext, secret string) (string, error) {
    if plaintext == "" {
        return "", nil  // 空明文直接返回空串
    }

    // 步骤 1：生成 16 字节随机 salt
    salt := make([]byte, saltSize)
    io.ReadFull(rand.Reader, salt)

    // 步骤 2：HKDF 派生 SM4 密钥
    block, _ := NewCipher(DeriveKeyHKDF(secret, salt))

    // 步骤 3：创建 GCM 实例
    gcm, _ := cipher.NewGCM(block)

    // 步骤 4：生成 12 字节随机 Nonce
    nonce := make([]byte, gcm.NonceSize())
    io.ReadFull(rand.Reader, nonce)

    // 步骤 5：SM4-GCM-Seal（加密 + 认证）
    // nonce 同时作为「随机数」和「输出前缀」，Seal 将密文追加到 nonce 后
    sealed := gcm.Seal(nonce, nonce, []byte(plaintext), []byte(EncryptedPrefixV2))
    // sealed = nonce + 密文 + Tag

    // 步骤 6：组装输出
    enc := EncryptedPrefixV2 + base64.StdEncoding.EncodeToString(
        append(salt, sealed...))
    // 输出 = "enc:v2:" + Base64(salt + nonce + 密文 + Tag)
    return enc, nil
}
```

**内存布局图**：

```
Base64 解码后：
┌──────────┬──────────┬───────────────────┬──────────┐
│ salt     │ nonce    │ SM4 密文          │ Tag      │
│ 16 bytes │ 12 bytes │ len(plaintext) B  │ 16 bytes │
└──────────┴──────────┴───────────────────┴──────────┘

GCM.Seal 输入：
  key   = DeriveKeyHKDF(secret, salt)  // 16 字节
  nonce = 12 字节随机数
  aad   = "enc:v2:"                     // 7 字节
  plain = 原始明文

GCM.Seal 输出：
  sealed = nonce + ciphertext + tag     // 拼接后 Base64 编码
```

### 8.3 decryptV2/decryptV1 分派策略

```go
func DecryptString(ciphertext, secret string) (string, error) {
    switch {
    case ciphertext == "":
        return "", nil
    case strings.HasPrefix(ciphertext, EncryptedPrefixV3):  // "enc:v3:"
        return decryptV3(ciphertext, secret)
    case strings.HasPrefix(ciphertext, EncryptedPrefixV2):  // "enc:v2:"
        return decryptV2(ciphertext, secret)
    case strings.HasPrefix(ciphertext, EncryptedPrefix):    // "enc:v1:"
        return decryptV1(ciphertext, secret)
    default:
        return "", ErrUnencryptedValue  // 无前缀 = 被篡改/降级
    }
}
```

**分派顺序的重要性**：
- 必须先检查 `enc:v3:` 再检查 `enc:v2:` 再检查 `enc:v1:`
- 因为 `enc:v1:` 是 `enc:v1` 的前缀，如果先检查 v1 会误匹配 v10/v11 等未来版本
- 实际上 v3 前缀是 `enc:v3:`，v2 是 `enc:v2:`，不会互相匹配，但保持从新到旧的顺序是良好实践

### 8.4 密钥版本注册表（G-08 密钥轮换）

```go
type KeyVersion struct {
    Version   string // 版本标识（如 "v1", "v2", "20250901"）
    Key       []byte // 密钥材料
    Active    bool   // 是否为当前活跃密钥（写入使用）
    CreatedAt int64  // 创建时间
}

var keyRegistry []*KeyVersion
var keyRegistryMu sync.RWMutex

func RegisterKeyVersion(version string, key []byte, active bool) {
    keyRegistryMu.Lock()
    defer keyRegistryMu.Unlock()
    // ... 注册逻辑 ...
    if active {
        // 若设为 active，取消其他 active 标记
        // 保证任何时刻只有一个活跃版本
    }
}
```

**密钥轮换流程**：

```
时间线：
  T0: 注册 V1 (active=true)  → 所有加密使用 V1
  T1: 注册 V2 (active=true)  → V1 变 inactive，新加密使用 V2
       V1 仍可用于解密      → 历史数据可继续读取
  T2: 注册 V3 (active=true)  → V2 变 inactive，新加密使用 V3
       V1, V2 仍可用于解密  → 所有历史数据可读
```

### 8.5 密码操作审计日志（G-13）

三级等保要求密码操作本身也要有审计记录：

```go
type CryptoAuditEvent struct {
    Operation  string // "sm4_encrypt", "sm4_decrypt", "sm3_hash"
    Timestamp  int64  // Unix nano
    KeyVersion string // 使用的密钥版本
    InputLen   int    // 输入数据长度（不记录明文内容！）
    Success    bool   // 操作是否成功
    Error      string // 错误信息
}
```

**设计要点**：
- 只记录 `InputLen`，不记录明文/密文内容（防止审计日志泄露敏感数据）
- 记录 `KeyVersion`，支持密钥轮换后的审计追溯
- 记录 `Success` 和 `Error`，便于检测异常解密尝试

---

## 第 9 章：flusher.go 微批刷盘器完整生命周期走读

`pkg/store/flusher/flusher.go`（817 行）是审计存储的性能层，将高频单条写入折叠为低频批量提交，同时保证哈希链的严格 FIFO 顺序。

### 9.1 SaveLogWithSnapshot 单一权威入队全流程

```
调用方                    BufferedAuditStore                    底层存储
  │                              │                                │
  │── SaveLogWithSnapshot(log) ──▶│                                │
  │                              │                                │
  │                    ┌─────────┤                                │
  │                    │ 1. 检查 retryBacklog 是否饱和            │
  │                    │    ≥ MaxStaged → ErrBacklogSaturated     │
  │                    └─────────┤                                │
  │                              │                                │
  │                    ┌─────────┤                                │
  │                    │ 2. stateMu.Lock()                        │
  │                    │    检查 closed → ErrStoreClosed          │
  │                    └─────────┤                                │
  │                              │                                │
  │                    ┌─────────┤                                │
  │                    │ 3. 单一权威裁定                           │
  │                    │    log.PrevHash = b.lastHash             │
  │                    │    log.IntegrityHash = Compute(...)       │
  │                    │    log.SM2Signature = Sign(...)           │
  │                    └─────────┤                                │
  │                              │                                │
  │                    ┌─────────┤                                │
  │                    │ 4. 快照对齐（如有）                       │
  │                    │    snapshot.PrevHash = log.IntegrityHash  │
  │                    │    snapshot.IntegrityHash = ComputeSnap() │
  │                    └─────────┤                                │
  │                              │                                │
  │                    ┌─────────┤                                │
  │                    │ 5. 尝试入队                               │
  │                    │    非阻塞写入 → 成功则跳过等待            │
  │                    │    队列满 → EnqueueTimeout 有界等待       │
  │                    │    超时 → ErrBacklogSaturated             │
  │                    └─────────┤                                │
  │                              │                                │
  │                    ┌─────────┤                                │
  │                    │ 6. 链推进                                   │
  │                    │    b.lastHash = log.IntegrityHash         │
  │                    │    b.latest.Store(&logCopy)               │
  │                    │    stateMu.Unlock()                       │
  │                    └─────────┤                                │
  │                              │                                │
  │                    ┌─────────┤                                │
  │                    │ 7. 暂存 + 回写                             │
  │                    │    stageLog(&logCopy)                     │
  │                    │    *log = logCopy  ← 回写调用方指针       │
  │                    └─────────┤                                │
  │                              │                                │
  │◀── nil (成功) ────────────────│                                │
  │                              │                                │
  │                    (后台 goroutine)                             │
  │                              │── ticker ──▶ SaveLogsBatch() ──▶│
```

**核心不变量**：
- 步骤 3-6 在同一个 `stateMu` 临界区内完成，保证哈希链递进与入队成功的原子性
- 入队失败的记录不会推进 `lastHash`，磁盘链保持连续
- 调用方传入的 `prev_hash` 被强制覆盖（「单一权威」语义）

### 9.2 flushWorker 事件循环深度析

```go
func (b *BufferedAuditStore) flushWorker() {
    ticker := time.NewTicker(b.cfg.FlushInterval) // 默认 20ms
    
    for {
        select {
        case <-b.stopCh:
            // 优雅停机：排空队列后退出
            drainQueue(0)
            flushCurrent()
            return

        case req := <-b.flushReqCh:
            // Flush 屏障：循环排空直到队列和 backlog 都清空
            for {
                drainQueue(0)
                err := flushCurrent()
                if err != nil || (QueueDepth() == 0 && !hasBacklog()) {
                    req.done <- err
                    break
                }
            }

        case <-ticker.C:
            // 定时刷盘：每 20ms 尝试提交当前批次
            flushCurrent()

        case item := <-b.queue:
            // 队列项：累积到批次，达到 MaxBatchSize 时立即刷盘
            batchLogs = append(batchLogs, *item.log)
            if len(batchLogs) >= b.cfg.MaxBatchSize && !hasBacklog() {
                flushCurrent()
            }
        }
    }
}
```

**4 种事件的优先级**：
- `stopCh` > `flushReqCh` > `ticker.C` = `queue`（Go select 随机选择同优先级 case）
- 停机信号和 Flush 屏障优先于常规刷盘

### 9.3 退避重试与 backlog 保留机制

```
正常路径：
  batch ──SaveLogsBatch──▶ DB ✓ ──▶ unstageLogs ──▶ 清空 batch

失败路径：
  batch ──SaveLogsBatch──▶ DB ✗
    │
    ├── attempt 0: 失败 → sleep 25ms
    ├── attempt 1: 失败 → sleep 50ms
    ├── attempt 2: 失败 → sleep 100ms
    └── attempt 3: 失败 → 整批保留到 backlogLogs
          │
          ├── b.retryPending.Store(len(backlogLogs))
          ├── b.hasFlushError.Store(true)
          └── 下一轮 flush 时优先重投 backlog
```

**退避公式**：`25ms × 2^attempt`（第 0 次 25ms，第 1 次 50ms，第 2 次 100ms）

**backlog 优先重投**：下一轮刷盘时，backlog 中的记录会与新批次合并，backlog 在前、新批次在后，保证 FIFO 顺序。

### 9.4 Flush 强一致性屏障协议

```
Flush() 调用方                   flushWorker
    │                              │
    │── flushReq{done: ch} ──────▶│
    │                              │
    │                    ┌─────────┤
    │                    │ 循环 {                                         │
    │                    │   drainQueue(0)  ← 排空队列到批次              │
    │                    │   flushCurrent()  ← 尝试提交                  │
    │                    │   成功 + 无 backlog → break                    │
    │                    │   失败 → 继续循环（直到 FlushTimeout）         │
    │                    │ }                                              │
    │                    └─────────┤                                │
    │                              │                                │
    │◀── ch <- err ─────────────────│                                │
    │                              │                                │
    │  err == nil ⟺ 队列 + backlog 全部成功提交              │
```

**关键语义**：`Flush()` 返回 nil 当且仅当所有已确认记录都已成功落盘。这确保 `ListLogs` / `VerifyChain` / `GetStats` 等读路径不会在数据尚未落盘时给出虚假结论。

### 9.5 Close 无竞态停机协议

```
Close() 调用
    │
    ├── 1. stateMu.Lock()
    │      closed = true
    │      stateMu.Unlock()
    │      （后续 SaveLog 立即返回 ErrStoreClosed）
    │
    ├── 2. close(stopCh)
    │      （通知 flushWorker 停止 ticker，触发排空）
    │
    ├── 3. 等待 flushWorker 排空
    │      ├── 成功排空 → 继续
    │      └── 超时 (CloseTimeout = 10s) →
    │            报告搁浅条数，不关闭底层存储
    │            （避免被抛弃的工作线程写入已关闭句柄）
    │
    ├── 4. 关闭底层存储
    │      ├── auditStoreCloser → Close() error
    │      ├── auditStoreSimpleCloser → Close()
    │      └── io.Closer → Close() error
    │
    └── 5. 清空暂存区
           recentLogs = make(map...)
           stagedIDs = nil
```

### 9.6 stageLog 读己之写暂存与有界淘汰

`stageLog` 实现「读己之写」(Read-your-own-writes) 语义：调用方 `SaveLog` 成功后立即 `GetLog(id)` 应能读到刚写入的记录，即使它尚未落盘。

```go
func (b *BufferedAuditStore) stageLog(l *store.AuditLog) {
    b.stageMu.Lock()
    if _, dup := b.recentLogs[l.ID]; !dup {
        b.stagedIDs = append(b.stagedIDs, l.ID) // 记录插入顺序
    }
    b.recentLogs[l.ID] = l

    // 有界淘汰：当暂存区超过 MaxStaged 时，按插入顺序淘汰最旧条目
    if len(b.stagedIDs) > 2*b.cfg.MaxStaged || len(b.recentLogs) > b.cfg.MaxStaged {
        // 1. 压缩已删除的 ID 引用
        kept := b.stagedIDs[:0]
        for _, id := range b.stagedIDs {
            if _, ok := b.recentLogs[id]; ok {
                kept = append(kept, id)
            }
        }
        b.stagedIDs = kept
        // 2. 按 FIFO 顺序淘汰超出上限的条目
        for len(b.recentLogs) > b.cfg.MaxStaged && len(b.stagedIDs) > 0 {
            delete(b.recentLogs, b.stagedIDs[0])
            b.stagedIDs = b.stagedIDs[1:]
            b.evictedTotal.Add(1)
        }
    }
    b.stageMu.Unlock()
}
```

**淘汰策略**：FIFO（先进先出），最旧入队的记录最先被淘汰。这与 LRU 不同，因为审计日志的访问模式是「写入后短期可能读取，之后很少再读」。

---

## 第 10 章：存储后端实现与 repairchain 工具深度 Review

### 10.1 SQLite 存储实现关键设计

SQLite 是默认的生产存储后端（`pkg/store/sqlite/audit.go` 896 行 + `init.go` 502 行）：

**WAL 模式**：
```sql
PRAGMA journal_mode=WAL;      -- Write-Ahead Logging，允许并发读写
PRAGMA synchronous=NORMAL;    -- 平衡性能与安全
PRAGMA busy_timeout=5000;     -- 写锁等待超时 5 秒
```

**`BEGIN IMMEDIATE` 事务**：
```go
func (s *SQLiteAuditStore) SaveLogsBatch(logs []store.AuditLog, snaps []store.SnapshotRecord) error {
    tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
    // BEGIN IMMEDIATE 立即获取写锁，防止写锁升级死锁
    // ... 批量 INSERT ...
    return tx.Commit()
}
```

**连接池限制**：`sql.DB.SetMaxOpenConns(1)` — SQLite 只允许一个写连接，多写连接会导致 `database is locked` 错误。

**Schema 设计**：
```sql
CREATE TABLE audit_logs (
    seq             INTEGER PRIMARY KEY AUTOINCREMENT,  -- 单调递增链序
    id              TEXT NOT NULL UNIQUE,
    timestamp       TEXT NOT NULL,
    algorithm       TEXT NOT NULL DEFAULT 'SM3',
    integrity_hash  TEXT NOT NULL,
    prev_hash       TEXT NOT NULL DEFAULT '',
    -- ... 其他业务字段 ...
);

CREATE INDEX idx_audit_logs_timestamp ON audit_logs(timestamp);
CREATE INDEX idx_audit_logs_task_id ON audit_logs(task_id);
```

### 10.2 PostgreSQL 存储实现关键设计

PostgreSQL 用于高可用生产部署（`pkg/store/postgres/audit.go` 1074 行）：

**写入只角色**：
```sql
-- 连接时使用 writeonly 角色，只能 INSERT/UPDATE，不能 SELECT
CREATE ROLE audit_writeonly WITH LOGIN PASSWORD 'xxx';
GRANT INSERT, UPDATE ON audit_logs TO audit_writeonly;
-- 防止审计数据通过 SQL 注入泄露
```

**连接池配置**：
```go
db.SetMaxOpenConns(25)            // 最大 25 个连接
db.SetMaxIdleConns(10)            // 保持 10 个空闲连接
db.SetConnMaxLifetime(5 * time.Minute) // 连接最大生命周期 5 分钟
```

**分区表**：审计日志按月分区（`audit_logs_2026_09`），提升查询性能与清理效率。过期数据可以直接 DROP 分区，比逐行 DELETE 快几个数量级。

### 10.3 内存存储实现与测试一致性

内存存储（`pkg/store/memory/memory.go` 914 行）用于单元测试与开发调试：

- 使用 `sync.RWMutex` 保护内存数据结构
- 实现了完整的 `AuditStore` 接口，确保测试与生产行为一致
- 链序通过切片入队序保证（与 SQLite 的 `seq AUTOINCREMENT` 语义一致）
- 进程重启后数据丢失

### 10.4 repairchain 哈希链重签工具完整走读

`pkg/store/cmd/repairchain/main.go`（465 行）是离线工具，用于在算法升级后重新签名历史记录。

**工作流程**：

```
1. 连接数据库（SQLite 或 PostgreSQL）
2. 按规范化链序读取所有审计记录
   ORDER BY seq ASC, timestamp ASC, id ASC
3. 逐条核验：
   ├── 命中当前写入口径 → 跳过（已是规范态）
   ├── 命中历史候选 → 标记为「待重签」
   └── 全部不命中 → 标记为「已篡改」
4. 对待重签记录：
   a. 用当前写入口径重新计算 integrity_hash
   b. 批量 UPDATE 数据库
5. 输出报告：
   - 总记录数
   - 已规范态数
   - 重签数
   - 疑似篡改数
```

**使用方式**：
```bash
# 预览模式（不实际修改）
go run ./pkg/store/cmd/repairchain -sqlite-path /data/audit.db -dry-run

# 执行重签
go run ./pkg/store/cmd/repairchain -sqlite-path /data/audit.db

# PostgreSQL
go run ./pkg/store/cmd/repairchain -postgres-dsn "postgresql://..."
```

---

## 第 11 章：代码走读指南

**推荐走读顺序**：

1. **先读测试**：`audit_hash_test.go` → `envelope_test.go` → `flusher_test.go`
   - 理解每个模块的预期行为与边界条件
2. **再读接口**：`store.go` 中的 `AuditStore` 接口定义
   - 理解各方法的语义约束与前置/后置条件
3. **然后读实现**：
   - `audit_hash.go`：从 `integrityPayload` 开始，理解 9 要素拼接
   - `envelope.go`：从 `Seal` 开始，理解加密全流程
   - `flusher.go`：从 `SaveLog` 开始，理解入队到落盘的完整路径
4. **最后读工具**：`repairchain/main.go`
   - 理解哈希链重签的完整工作流

**Review 标注规范**：

对不理解或有疑问的代码段添加 `// REVIEW:` 注释，格式：

```go
// REVIEW: [问题类型] 问题描述
// 例如：
// REVIEW: [安全性] 此处是否需要检查 timestamp 的单调递增？
// REVIEW: [性能] 此处每次调用都分配新 map，是否可以复用？
// REVIEW: [正确性] 此处 HMAC 的 key 和 message 参数顺序是否正确？
```

---

## 第 12 章：常见问题与排查指南

### Q1：核验时出现 `ChainReasonBrokenChain` 断链

**可能原因**：
1. 数据库中存在手动修改或删除的记录
2. 算法升级后未执行 `repairchain` 重签
3. 时区归一化 bug（早期版本未 UTC 归一化）

**排查步骤**：
```bash
# 1. 运行链核验
curl -s http://127.0.0.1:8084/api/audit/verify-chain | jq .

# 2. 查看断链位置
# 响应中的 first_broken_seq 字段标识第一条断链记录的序号

# 3. 执行重签（如需要）
go run ./pkg/store/cmd/repairchain -sqlite-path /data/audit.db -dry-run
```

### Q2：解密时出现 `ErrUnencryptedValue`

**可能原因**：
1. 数据在写入时未加密（空密钥导致 fail-closed 拒绝）
2. 密文版本前缀被篡改或剥离
3. 数据库中存在历史明文数据

**排查步骤**：
```bash
# 检查数据库中是否存在无前缀的记录
sqlite3 /data/audit.db \
  "SELECT id, snapshot_data FROM audit_snapshots WHERE snapshot_data NOT LIKE 'enc:%' LIMIT 10;"
```

### Q3：刷盘器积压持续增长

**可能原因**：
1. 底层数据库写入性能不足
2. 单批 `MaxBatchSize` 设置过小
3. `FlushInterval` 设置过长

**排查步骤**：
```bash
# 查看刷盘器指标
curl -s http://127.0.0.1:8084/metrics | grep audit_flush

# 关注以下指标：
# audit_flush_queue_depth — 当前队列深度
# audit_flush_retry_pending — 重试暂存区深度
# audit_flush_overflow_total — 溢出拒绝总数
```

---

## 第 13 章：术语表

| 术语 | 英文 | 含义 |
|---|---|---|
| 前映像 | Pre-image | 哈希计算前的原始输入字符串 |
| 完整性哈希 | Integrity Hash | 审计日志的防篡改哈希值 |
| 密钥化存证 | Keyed Attestation | 使用 HMAC-SM3 密钥计算的存证哈希 |
| 信封加密 | Envelope Encryption | 将密钥与密文打包在一起的加密格式 |
| 微批刷盘 | Micro-batch Flush | 将频繁小事务折叠为低频大事务的写入优化 |
| 单一权威 | Single Authority | 链尾哈希由刷盘器在服务端单点裁定 |
| 规范化链序 | Canonical Chain Order | `(seq ASC, timestamp ASC, id ASC)` 排序规则 |
| 伪分叉 | False Fork | 因时区不一致导致的哈希不匹配（非真正篡改） |
| Fail-closed | Fail-closed | 异常时拒绝而非降级为不安全行为 |
| HKDF | HMAC-based Key Derivation | 基于 HMAC 的密钥派生函数 |
| AEAD | Authenticated Encryption with Associated Data | 认证加密（同时提供机密性与完整性） |

---

## 第 14 章：Review 检查清单详细版

以下清单为每个模块的具体检查点，Review 时逐项确认。

### 哈希链模块检查清单

#### `pkg/store/audit_hash.go`

- [ ] `integrityPayload` 9 要素拼接顺序与文档一致
- [ ] UTC 归一化：`inUTC=true` 时使用 `.UTC().Format()`
- [ ] `ComputeAuditIntegrityHash` 密钥化/无密钥双路径
- [ ] `VerifyAuditIntegrityHash` 5 种候选全部覆盖
- [ ] `hmac.Equal` 常量时间比较（非 `==`）
- [ ] `hashCandidate` 结构体的 `payload` 字段在闭包外捕获（防止闭包捕获循环变量）
- [ ] `LocalTZ` 候选只在 `local != utc` 时添加（避免重复计算）
- [ ] `IsCanonicalHashLabel` 与当前写入口径一致
- [ ] 快照独立完整性哈希（8 要素，包含 inputSample/outputSample）
- [ ] SM2 签名器/验签器接口化设计，未配置时向下兼容

### 信封加密模块检查清单

#### `pkg/crypto/envelope.go`

- [ ] `EncryptString` 空密钥返回 `ErrEmptyKey`（不静默降级）
- [ ] `encryptV2` 生成 16B salt + 12B nonce
- [ ] `DeriveKeyHKDF` Extract + Expand 两阶段
- [ ] 版本前缀参与 AAD（`enc:v2:` 作为 GCM AAD）
- [ ] `DecryptString` 分派顺序：v3 → v2 → v1 → 无前缀报错
- [ ] `decryptV1` 使用旧派生 `DeriveKey`（SHA-256 截断）
- [ ] `IsEncrypted` 检查所有已知前缀
- [ ] 密钥版本注册表 `RegisterKeyVersion` 保证只有一个 active
- [ ] 密码操作审计日志不记录明文内容

#### `pkg/crypto/sm3.go`

- [ ] SM3 初始向量与国标 GB/T 32918.4-2016 一致
- [ ] `block` 函数 64 轮压缩（前 16 轮异或型 + 后 48 轮择一型）
- [ ] `checkSum` 填充到 56 mod 64 字节
- [ ] `HMACSM3` 使用标准 `crypto/hmac` 包构造

#### `pkg/crypto/sm4.go`

- [ ] S 盒 256 字节与国标 GB/T 32907-2016 一致
- [ ] `cryptBlock` 32 轮复合变换 + 反序输出
- [ ] `expandKey` 加密轮密钥正序、解密轮密钥逆序
- [ ] `NewCipher` 检查密钥长度必须 16 字节

### 微批刷盘器检查清单

#### `pkg/store/flusher/flusher.go`

- [ ] `SaveLogWithSnapshot` 在 `stateMu` 临界区内完成链推进 + 入队
- [ ] 强制覆盖调用方传入的 `prev_hash`（单一权威）
- [ ] 快照 `PrevHash` 指向主日志 `IntegrityHash`（子链锚定）
- [ ] 入队超时后不推进 `lastHash`（链保持连续）
- [ ] `flushWorker` 4 种事件正确处理
- [ ] 退避重试公式 `25ms * 2^attempt`
- [ ] backlog 优先重投（FIFO 顺序）
- [ ] `Flush` 屏障循环排空直到队列 + backlog 都清空
- [ ] `Close` 先置 `closed=true` 再 `close(stopCh)`
- [ ] 排空超时不关闭底层存储（防止工作线程写入已关闭句柄）
- [ ] `stageLog` 有界淘汰按 FIFO 顺序
- [ ] `GetLog` 优先从暂存区读取（读己之写）
- [ ] `ListLogs` / `VerifyChain` / `GetStats` 先 `Flush` 再读取

### 存储后端检查清单

#### SQLite (`pkg/store/sqlite/`)

- [ ] WAL 模式启用
- [ ] `BEGIN IMMEDIATE` 事务（防写锁升级死锁）
- [ ] `SetMaxOpenConns(1)`（单写连接）
- [ ] Schema 包含 `seq AUTOINCREMENT` 字段
- [ ] 规范化链序 `ORDER BY seq ASC, timestamp ASC, id ASC`

#### PostgreSQL (`pkg/store/postgres/`)

- [ ] 连接池参数：MaxOpen=25, MaxIdle=10, MaxLifetime=5min
- [ ] 分区表支持（按月分区）
- [ ] `VerifyChain` 使用与 SQLite 相同的规范化链序

#### 内存 (`pkg/store/memory/`)

- [ ] `sync.RWMutex` 保护所有读写操作
- [ ] 实现完整的 `AuditStore` 接口
- [ ] 链序通过切片入队序保证

---

## 第 15 章：周交付物清单

### 交付物 1：审计不可篡改体系 Review 笔记

- [ ] 9 要素前映像结构设计原理分析（为何用 `|` 分隔而非 JSON）
- [ ] UTC 归一化伪分叉问题分析报告
- [ ] 5 种核验候选的演进历史与安全语义
- [ ] SM4-GCM v1→v2→v3 格式演进对比分析
- [ ] HKDF 密钥派生 vs v1 SHA-256 截断的安全性对比
- [ ] 单一权威机制设计原理（为何不能由调用方传入 prev_hash）
- [ ] 微批刷盘器 FIFO 保序与退避重试机制分析
- [ ] 快照独立完整性哈希设计原理（旧实现的安全缺陷）

### 交付物 2：发现的问题清单与改进建议

| 优先级 | 问题 | 位置 | 状态 | 建议 |
|---|---|---|---|---|
| P0 | SM2 签名器未注册时静默返回空串 | `audit_hash.go:332` | 设计决策 | 文档明确说明 G-10 为可选增强 |
| P1 | `flushWorker` 中 `select` 同优先级 case 随机选择 | `flusher.go:772` | 已确认 | 停机信号可能被延迟一个 ticker 周期 |
| P1 | `stageLog` 淘汰时遍历 `stagedIDs` 压缩 | `flusher.go:645` | 待优化 | 高暂存场景可考虑双队列设计 |
| P2 | v1 密文解密使用无 salt 弱派生 | `envelope.go:463` | 设计决策 | 存量数据迁移工具待开发 |
| P2 | `repairchain` 工具无并发控制 | `cmd/repairchain/main.go` | 待评估 | 多副本部署需分布式锁 |
| P3 | 内存存储 `VerifyChain` 未使用规范化链序 | `memory/memory.go` | 待修复 | 应与 SQLite/PostgreSQL 保持一致 |

### 交付物 3：密码学安全审计报告

- [ ] SM3 实现与国标测试向量验证报告
- [ ] SM4 实现性能基准测试（吞吐量、延迟）
- [ ] GCM Nonce 复用风险分析
- [ ] 密钥轮换流程操作手册
- [ ] 审计存证端到端安全性验证方案

---

## 附录 A：关键环境变量速查表

| 环境变量 | 默认值 | 说明 |
|---|---|---|
| `AUDIT_LOG_HASH_KEY` | `""` | HMAC-SM3 密钥化存证密钥（空 = 无密钥 SM3） |
| `AUDIT_LOG_ENCRYPTION_KEY` | `""` | SM4-GCM 信封加密密钥（空 = fail-closed 拒绝加密） |
| `AUDIT_STORE_TYPE` | `"sqlite"` | 存储后端类型（`sqlite` / `postgres` / `memory`） |
| `AUDIT_SQLITE_PATH` | `./audit.db` | SQLite 数据库文件路径 |
| `AUDIT_POSTGRES_DSN` | `""` | PostgreSQL 连接字符串 |
| `AUDIT_FLUSH_BUFFER_SIZE` | `10000` | 刷盘器缓冲队列容量 |
| `AUDIT_FLUSH_MAX_BATCH` | `200` | 单批最大写入条数 |
| `AUDIT_FLUSH_INTERVAL` | `20ms` | 刷盘间隔 |
| `AUDIT_FLUSH_ENQUEUE_TIMEOUT` | `500ms` | 入队等待超时 |
| `AUDIT_FLUSH_CLOSE_TIMEOUT` | `10s` | 优雅停机排空超时 |
| `AUDIT_FLUSH_MAX_STAGED` | `50000` | 暂存区上限 |

---

## 附录 B：密码学算法参数速查

| 算法 | 标准编号 | 输出长度 | 密钥长度 | 分组长度 |
|---|---|---|---|---|
| SM3 | GM/T 0004-2012 | 256 位 | N/A（杂凑） | 512 位（消息分组） |
| SM4 | GB/T 32907-2016 | N/A（分组密码） | 128 位 | 128 位 |
| SM4-GCM | — | 密文 + 128 位 Tag | 128 位 | 128 位 |
| HMAC-SM3 | — | 256 位 | 任意 | N/A |
| HKDF-SM3 | RFC 5869 | 可配置（本项目 128 位） | 任意 | N/A |
| SHA-256 | FIPS 180-4 | 256 位 | N/A（杂凑） | 512 位 |

---

## 附录 C：存储接口关系全景图

```
                    ┌─────────────────────────────┐
                    │      AuditStore 接口         │
                    │  (pkg/store/store.go)         │
                    └──────────┬──────────────────┘
                               │
              ┌────────────────┼────────────────┐
              │                │                │
    ┌─────────▼──────┐ ┌──────▼───────┐ ┌──────▼──────┐
    │ SQLiteAudit    │ │ PostgresAudit│ │ MemoryAudit │
    │ Store          │ │ Store        │ │ Store       │
    └────────┬───────┘ └──────┬───────┘ └──────┬──────┘
             │                │                │
             └────────────────┼────────────────┘
                              │
                    ┌─────────▼──────────────┐
                    │  BufferedAuditStore     │
                    │  (装饰器模式包装)        │
                    │                         │
                    │  + 微批刷盘              │
                    │  + 单一权威哈希          │
                    │  + FIFO 保序             │
                    │  + 重试暂存              │
                    │  + 优雅停机              │
                    └─────────┬──────────────┘
                              │
                    ┌─────────▼──────────────┐
                    │   audit-log 服务        │
                    │   (services/audit-log)  │
                    └────────────────────────┘
```

---

## 附录 D：审计存证安全设计全景图

```
┌─────────────────────────────────────────────────────────────────────┐
│                     审计存证安全设计全景图                           │
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  ┌─────────────────────────────────────────────────────────────┐  │
│  │ ① 不可篡改哈希链 (audit_hash.go)                           │  │
│  │    ├── 9 要素前映像 → SM3/HMAC-SM3 → 64 字符 hex           │  │
│  │    ├── 链式绑定：Record[n].prev_hash = Record[n-1].hash    │  │
│  │    └── 多轨核验：5 种候选向下兼容                           │  │
│  └───────────────────────────┬─────────────────────────────────┘  │
│                               │                                     │
│  ┌───────────────────────────▼─────────────────────────────────┐  │
│  │ ② 微批刷盘器 (flusher.go)                                   │  │
│  │    ├── 单一权威：stateMu 临界区内链推进 + 入队             │  │
│  │    ├── FIFO 保序：严格队列序 + 退避重试                    │  │
│  │    └── 强一致性屏障：Flush → 队列 + backlog 全清空         │  │
│  └───────────────────────────┬─────────────────────────────────┘  │
│                               │                                     │
│  ┌───────────────────────────▼─────────────────────────────────┐  │
│  │ ③ 信封加密 (envelope.go)                                    │  │
│  │    ├── SM4-GCM AEAD：机密性 + 完整性 + AAD 绑定            │  │
│  │    ├── HKDF-SM3 派生：逐记录 salt + 用途绑定               │  │
│  │    └── 密钥版本注册表：v3 格式支持轮换                     │  │
│  └───────────────────────────┬─────────────────────────────────┘  │
│                               │                                     │
│  ┌───────────────────────────▼─────────────────────────────────┐  │
│  │ ④ 存储后端 (sqlite / postgres / memory)                     │  │
│  │    ├── SQLite：WAL + BEGIN IMMEDIATE + 单写连接             │  │
│  │    ├── PostgreSQL：分区表 + 写入只角色 + 连接池              │  │
│  │    └── 分层降级：PostgreSQL → SQLite → 内存                 │  │
│  └───────────────────────────┬─────────────────────────────────┘  │
│                               │                                     │
│  ┌───────────────────────────▼─────────────────────────────────┐  │
│  │ ⑤ SM2 数字签名 (G-10 审计不可否认性)                        │  │
│  │    ├── 签名对象 = integrity_hash（非原始数据）               │  │
│  │    ├── 接口化设计：SM2Signer / SM2Verifier                  │  │
│  │    └── 未配置时向下兼容（不影响哈希链核验）                 │  │
│  └─────────────────────────────────────────────────────────────┘  │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 附录 E：推荐阅读与延伸阅读

### 必读

1. **GM/T 0004-2012《SM3 密码杂凑算法》** — 理解 SM3 算法的完整规范
2. **GB/T 32907-2016《SM4 分组密码算法》** — 理解 SM4 的轮函数与密钥扩展
3. **RFC 5869《HMAC-based Extract-and-Expand Key Derivation Function (HKDF)》** — 理解密钥派生的两阶段设计
4. **RFC 2104《HMAC: Keyed-Hashing for Message Authentication》** — 理解 HMAC 的安全构造
5. **NIST SP 800-38D《GCM Mode》** — 理解 GCM 认证加密的工作原理

### 选读

6. **《密码学导引》** — 汪小帆等，密码学基础理论
7. **《Go 并发编程实战》** — 理解 Go 并发原语的正确使用
8. **PostgreSQL `FOR UPDATE SKIP LOCKED` 文档** — 理解多副本租约抢占的 SQL 语义
9. **SQLite WAL 模式文档** — 理解 WAL 模式的并发特性与限制

### 在线资源

10. [国密算法在线演示](https://gmssl.org/) — 可在线验证 SM3/SM4 计算结果
11. [RFC 5869 测试向量](https://www.rfc-editor.org/rfc/rfc5869#appendix-A) — HKDF 正确性验证
12. [GCM 安全性分析](https://csrc.nist.gov/publications/detail/sp/800-38d/final) — GCM Nonce 复用的灾难性后果
