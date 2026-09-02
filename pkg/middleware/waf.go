// Package middleware provides Web Application Firewall (WAF) middleware for attack detection.
// Package middleware 提供 Web 应用防火墙（WAF）攻击检测与拦截中间件。
//
// ==============================================================================
// 【WAF 纵深防御架构（三级等保 G-12 合规）】
// 基于预编译正则表达式引擎，对 HTTP 请求的 URL 路径、查询参数、请求头及请求体
// 进行多维度恶意载荷扫描，覆盖五大类常见 Web 攻击向量：
//
// 1. 【SQL 注入防护】：检测 UNION SELECT、OR 1=1、DROP TABLE 等经典注入模式；
// 2. 【XSS 跨站脚本防护】：检测 <script>、javascript:、onerror= 等脚本注入模式；
// 3. 【命令注入防护】：检测 |、;、&&、反引号、$() 等 Shell 命令拼接模式；
// 4. 【路径穿越防护】：检测 ../、..\、/etc/passwd 等目录遍历模式；
// 5. 【已知漏洞利用防护】：检测 Log4Shell (CVE-2021-44228) 等高危漏洞利用载荷。
//
// 检测命中后以标准 403 FORBIDDEN 错误信封中断请求，并通过结构化日志记录完整攻击上下文。
// ==============================================================================

package middleware

import (
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// wafRule 定义单条 WAF 检测规则，包含攻击类别标签、正则表达式集合与人类可读描述。
type wafRule struct {
	category    string           // 攻击类别标签（用于日志与告警分类）
	description string           // 规则中文描述（用于审计溯源）
	patterns    []*regexp.Regexp // 预编译正则表达式列表
}

// wafRules 存储所有预编译的 WAF 检测规则（包级不可变，init 时一次性构建）。
var wafRules []wafRule

func init() {
	// 辅助闭包：批量编译正则，失败时 panic（启动阶段快速暴露配置错误）
	compile := func(raw []string) []*regexp.Regexp {
		out := make([]*regexp.Regexp, len(raw))
		for i, r := range raw {
			out[i] = regexp.MustCompile(r)
		}
		return out
	}

	wafRules = []wafRule{
		// ── 1. SQL 注入检测规则 ──────────────────────────────────
		{
			category:    "SQL_INJECTION",
			description: "SQL 注入攻击检测",
			patterns: compile([]string{
				`(?i)\bunion\s+select\b`,            // UNION SELECT 联合查询注入
				`(?i)\bunion\s+all\s+select\b`,      // UNION ALL SELECT 联合查询注入
				`(?i)\bor\s+1\s*=\s*1\b`,            // OR 1=1 永真条件注入
				`(?i)\bor\s+'1'\s*=\s*'1'`,          // OR '1'='1' 字符串永真注入
				`(?i)\bor\s+"1"\s*=\s*"1"`,          // OR "1"="1" 双引号永真注入
				`(?i)\band\s+1\s*=\s*1\b`,           // AND 1=1 布尔盲注探测
				`(?i)\bdrop\s+table\b`,              // DROP TABLE 破坏性注入
				`(?i)\bdrop\s+database\b`,           // DROP DATABASE 破坏性注入
				`(?i)\btruncate\s+table\b`,          // TRUNCATE TABLE 破坏性注入
				`(?i)\binsert\s+into\b.*\bvalues\b`, // INSERT INTO ... VALUES 数据篡改
				`(?i)\bdelete\s+from\b`,             // DELETE FROM 数据删除注入
				`(?i);\s*select\b`,                  // 分号后接 SELECT 堆叠查询注入
				`(?i);\s*update\b.*\bset\b`,         // 分号后接 UPDATE...SET 堆叠注入
				`(?i)\bexec(\s+|\s*\()\s*xp_`,       // 执行 xp_cmdshell 等扩展存储过程
				`(?i)\bwaitfor\s+delay\b`,           // WAITFOR DELAY 时间盲注探测
				`(?i)\bbenchmark\s*\(`,              // BENCHMARK() MySQL 时间盲注
				`(?i)\bsleep\s*\(`,                  // SLEEP() MySQL 时间盲注
				`(?i)'\s*or\s+'`,                    // 单引号 OR 注入闭合
				`(?i)\bconcat\s*\(\s*char\s*\(`,     // CONCAT(CHAR()) 编码绕过注入
				`(?i)\bload_file\s*\(`,              // LOAD_FILE() 文件读取注入
				`(?i)\binto\s+outfile\b`,            // INTO OUTFILE 文件写入注入
				`(?i)\binformation_schema\b`,        // information_schema 元数据枚举
			}),
		},

		// ── 2. XSS 跨站脚本检测规则 ─────────────────────────────
		{
			category:    "XSS",
			description: "XSS 跨站脚本攻击检测",
			patterns: compile([]string{
				`(?i)<\s*script[^>]*>`,         // <script> 标签注入
				`(?i)javascript\s*:`,           // javascript: 伪协议
				`(?i)\bon\w+\s*=`,              // onerror= / onload= / onmouseover= 等事件处理器
				`(?i)<\s*img[^>]+onerror\s*=`,  // <img onerror=> 图片标签事件注入
				`(?i)<\s*iframe[^>]*>`,         // <iframe> 框架注入
				`(?i)<\s*object[^>]*>`,         // <object> 对象标签注入
				`(?i)<\s*embed[^>]*>`,          // <embed> 嵌入标签注入
				`(?i)<\s*svg[^>]*on\w+\s*=`,    // <svg onload=> SVG 事件注入
				`(?i)\balert\s*\(`,             // alert() 弹窗探测
				`(?i)\bprompt\s*\(`,            // prompt() 弹窗探测
				`(?i)\bconfirm\s*\(`,           // confirm() 弹窗探测
				`(?i)document\s*\.\s*cookie`,   // document.cookie 窃取
				`(?i)document\s*\.\s*location`, // document.location 重定向
				`(?i)window\s*\.\s*location`,   // window.location 重定向
				`(?i)eval\s*\(`,                // eval() 动态执行
				`(?i)expression\s*\(`,          // CSS expression() IE 注入
				`(?i)<\s*body[^>]+on\w+\s*=`,   // <body onload=> 标签事件注入
				`(?i)fromcharcode`,             // String.fromCharCode() 编码绕过
			}),
		},

		// ── 3. 命令注入检测规则 ─────────────────────────────────
		{
			category:    "COMMAND_INJECTION",
			description: "OS 命令注入攻击检测",
			patterns: compile([]string{
				`[|;]\s*\b(cat|ls|id|whoami|uname|wget|curl|nc|netcat|bash|sh|cmd|powershell)\b`, // 管道/分号后接系统命令
				`&&\s*\b(cat|ls|id|whoami|uname|wget|curl|nc|netcat|bash|sh|cmd|powershell)\b`,   // && 链式命令注入
				`\|\|\s*\b(cat|ls|id|whoami|uname|wget|curl)\b`,                                  // || 链式命令注入
				"`[^`]+`",               // 反引号命令替换
				`\$\([^)]+\)`,           // $() 命令替换
				`(?i)\bexec\s*\(`,       // exec() 函数调用
				`(?i)\bsystem\s*\(`,     // system() 函数调用
				`(?i)\bpassthru\s*\(`,   // passthru() PHP 命令执行
				`(?i)\bpopen\s*\(`,      // popen() 进程创建
				`(?i)\bproc_open\s*\(`,  // proc_open() 进程创建
				`(?i)\bshell_exec\s*\(`, // shell_exec() PHP 命令执行
				`(?i)Runtime\s*\.\s*getRuntime\s*\(\s*\)\s*\.\s*exec`, // Java Runtime.exec()
			}),
		},

		// ── 4. 路径穿越检测规则 ─────────────────────────────────
		{
			category:    "PATH_TRAVERSAL",
			description: "路径穿越攻击检测",
			patterns: compile([]string{
				`\.\./`,                   // ../ Unix 目录遍历
				`\.\.\\`,                  // ..\ Windows 目录遍历
				`(?i)%2e%2e%2f`,           // URL 编码 ../ (%2e%2e%2f)
				`(?i)%2e%2e/`,             // 部分编码 ../ (%2e%2e/)
				`(?i)\.\.%2f`,             // 部分编码 ../ (..%2f)
				`(?i)%2e%2e%5c`,           // URL 编码 ..\ (%2e%2e%5c)
				`(?i)\.\.%5c`,             // 部分编码 ..\ (..%5c)
				`(?i)/etc/passwd`,         // Linux 敏感文件读取
				`(?i)/etc/shadow`,         // Linux 密码文件读取
				`(?i)/etc/hosts`,          // Linux 主机配置读取
				`(?i)/proc/self/environ`,  // Linux 进程环境变量泄露
				`(?i)/proc/self/cmdline`,  // Linux 进程命令行泄露
				`(?i)\\windows\\system32`, // Windows 系统目录穿越
				`(?i)\\boot\.ini`,         // Windows 启动配置读取
				`(?i)%00`,                 // Null 字节截断
			}),
		},

		// ── 5. 已知漏洞利用检测规则 ─────────────────────────────
		{
			category:    "EXPLOIT",
			description: "已知高危漏洞利用检测",
			patterns: compile([]string{
				`\$\{jndi:(ldap[s]?|rmi|dns|iiop|corba|nds|http)://`, // Log4Shell CVE-2021-44228 JNDI 注入
				`\$\{(?:lower|upper|env|sys|java|date|ctx)\b`,        // Log4j  Lookup 表达式通用检测
				`(?i)\bshellshock\b`,                                 // Shellshock CVE-2014-6271
				`\(\)\s*\{[^}]*\}\s*;`,                               // Shellshock Bash 函数定义载荷
				`(?i)\$\{TMPL:`,                                      // Spring4Shell / Thymeleaf SSTI
				`(?i)#\{(?:T\(|Runtime)`,                             // Spring EL / OGNL 表达式注入
				`(?i)\.class\.forName\s*\(`,                          // Java 反射注入
			}),
		},
	}
}

// WAF returns a Web Application Firewall middleware that detects and blocks common web attacks.
//
// WAF 返回 Web 应用防火墙中间件，基于预编译正则引擎对请求进行多维度恶意载荷检测。
//
// 三级等保合规编号：G-12（Web 应用攻击防护）。
//
// 检测范围：
//  1. SQL 注入（UNION SELECT、永真条件、堆叠查询、时间盲注等 21 种模式）；
//  2. XSS 跨站脚本（<script>、javascript:、事件处理器、DOM 操作等 18 种模式）；
//  3. 命令注入（管道符、反引号、$()、危险函数调用等 12 种模式）；
//  4. 路径穿越（../、编码绕过、敏感文件访问、Null 截断等 14 种模式）；
//  5. 已知漏洞利用（Log4Shell、Shellshock、Spring4Shell 等 7 种模式）。
//
// 执行逻辑：
//  1. 依次扫描请求的 URL 路径、原始查询字符串、关键请求头（User-Agent、Referer、Cookie、Content-Type）；
//  2. 若 Content-Type 为表单类型（application/x-www-form-urlencoded 或 multipart/form-data），
//     则缓存并读取请求体进行扫描，随后通过 io.NopCloser 恢复请求体供后续 Handler 消费；
//  3. 任意检测维度命中即中断请求链，以结构化日志记录攻击类别、来源 IP、请求路径与载荷摘要，
//     并向客户端返回标准 403 FORBIDDEN 错误信封；
//  4. 全部维度通过则放行至下一中间件。
//
// 安全说明：
//   - 正则表达式在 init() 阶段一次性预编译，运行时零分配零编译开销；
//   - 请求体读取受 maxBodyScanSize（默认 1 MiB）限制，防止超大 Payload 引发内存压力；
//   - 请求体读取后通过 io.NopCloser + bytes.NewReader 重建，保证后续 Handler 可正常消费。
func WAF(logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}

	return func(c *gin.Context) {
		// ── 构建待扫描的有效载荷列表 ───────────────────────────
		type scanTarget struct {
			name    string // 载荷来源标识（用于日志定位）
			content string // 待检测的原始字符串
		}

		targets := make([]scanTarget, 0, 8)

		// 1. URL 路径
		if p := c.Request.URL.Path; p != "" {
			targets = append(targets, scanTarget{"url_path", p})
		}

		// 2. 原始查询字符串
		if qs := c.Request.URL.RawQuery; qs != "" {
			targets = append(targets, scanTarget{"query_string", qs})
		}

		// 3. 关键请求头（攻击者常通过头部注入载荷）
		for _, header := range wafInspectHeaders {
			if v := c.GetHeader(header); v != "" {
				targets = append(targets, scanTarget{"header:" + header, v})
			}
		}

		// 4. 请求体（仅扫描表单类型，避免对二进制大文件产生无效开销）
		if ct := c.GetHeader("Content-Type"); ct != "" {
			ctLower := strings.ToLower(ct)
			if strings.Contains(ctLower, "application/x-www-form-urlencoded") ||
				strings.Contains(ctLower, "multipart/form-data") {
				if c.Request.Body != nil {
					bodyBytes, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodyScanSize))
					if err == nil && len(bodyBytes) > 0 {
						targets = append(targets, scanTarget{"request_body", string(bodyBytes)})
					}
					// 重建请求体，保证后续 Handler 可正常读取
					c.Request.Body = io.NopCloser(
						io.MultiReader(
							strings.NewReader(string(bodyBytes)),
							c.Request.Body,
						),
					)
				}
			}
		}

		// ── 执行多规则多维度扫描 ───────────────────────────────
		for _, rule := range wafRules {
			for _, target := range targets {
				for _, pattern := range rule.patterns {
					if pattern.MatchString(target.content) {
						requestID := GetTraceID(c)
						clientIP := RealClientIP(c)

						// 截断载荷摘要，防止日志膨胀
						payload := target.content
						if len(payload) > maxPayloadLogLen {
							payload = payload[:maxPayloadLogLen] + "..."
						}

						logger.Warn("WAF attack detected",
							"request_id", requestID,
							"category", rule.category,
							"description", rule.description,
							"client_ip", clientIP,
							"method", c.Request.Method,
							"path", c.Request.URL.Path,
							"target", target.name,
							"payload", payload,
							"matched_pattern", pattern.String(),
						)

						AbortWithError(c, http.StatusForbidden,
							"WAF_BLOCKED",
							"Request blocked by WAF: malicious payload detected",
							gin.H{"category": rule.category},
						)
						return
					}
				}
			}
		}

		c.Next()
	}
}

// wafInspectHeaders 定义 WAF 需检查的请求头列表。
// 攻击者常通过 User-Agent、Referer、Cookie 等头部注入恶意载荷。
var wafInspectHeaders = []string{
	"User-Agent",
	"Referer",
	"Cookie",
	"X-Forwarded-For",
	"Content-Type",
}

const (
	// maxBodyScanSize WAF 请求体扫描最大字节数（默认 1 MiB），超出部分截断不扫描。
	maxBodyScanSize = 1 << 20

	// maxPayloadLogLen 日志中载荷摘要最大截断长度，防止超长 Payload 引发日志膨胀。
	maxPayloadLogLen = 512
)
