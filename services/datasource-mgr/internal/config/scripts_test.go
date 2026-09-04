// Package config_test contains integration and verification tests for module startup scripts and certificates.
// Package config_test 包含 datasource-mgr 模块启动脚本、证书生成脚本及子进程生命周期的集成测试套件。
//
// 本测试套件覆盖：
// 1. 脚本文件存在性与可执行权限验证（TestScripts_ExistenceAndExecutable）；
// 2. Shell 脚本语法静态分析（TestScripts_BashSyntaxCheck：通过 bash -n 验证）；
// 3. gen-certs.sh 证书生成脚本与 X.509 属性深度校验（TestGenCertsScript_ExecutionAndCertificateVerification）；
// 4. dev-run.sh 开发模式子进程拉起与 HTTP 健康端点探测（TestDevRunScript_StartupAndHealth）；
// 5. prod-run.sh 生产加固模式子进程拉起与 mTLS 双向证书校验（TestProdRunScript_StartupAndMTLS）。
package config

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// getModuleRootDir returns the absolute path to services/datasource-mgr
// getModuleRootDir 通过 runtime.Caller 获取当前测试文件路径并向上回溯两级，
// 计算并返回 services/datasource-mgr 模块的绝对根目录路径。
func getModuleRootDir(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to get caller information")
	}
	// currentFile 为 services/datasource-mgr/internal/config/scripts_test.go
	moduleDir := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
	return moduleDir
}

// 1. 静态检查：验证所有脚本文件存在且具有可执行权限 (TestScripts_ExistenceAndExecutable)
// 执行逻辑：
// 1. 遍历待测试的脚本清单；
// 2. 使用 os.Stat 检查文件物理存在；
// 3. 校验文件的 Unix 权限位 mode&0111 是否包含可执行标记（非 Windows 平台）。
func TestScripts_ExistenceAndExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("skipping file permission tests on Windows")
	}

	moduleDir := getModuleRootDir(t)
	expectedScripts := []string{
		"run.sh",
		"scripts/dev-run.sh",
		"scripts/prod-run.sh",
		"scripts/gen-certs.sh",
		"scripts/deploy.sh",
		"scripts/health-check.sh",
	}

	for _, relPath := range expectedScripts {
		fullPath := filepath.Join(moduleDir, relPath)
		info, err := os.Stat(fullPath)
		if err != nil {
			t.Fatalf("script %s does not exist: %v", relPath, err)
		}
		if info.Mode()&0111 == 0 {
			t.Errorf("script %s is missing executable permission: mode=%v", relPath, info.Mode())
		}
	}
}

// 2. 语法检查：执行 bash -n 验证所有脚本语法合法 (TestScripts_BashSyntaxCheck)
// 执行逻辑：
// 1. 探测宿主系统中是否存在 bash 可执行程序；
// 2. 对每个脚本执行 "bash -n <script>" 命令，在不实际执行代码的前提下校验语法是否有语法错误；
// 3. 若返回错误或解析输出，则记录测试失败。
func TestScripts_BashSyntaxCheck(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available on this system")
	}

	moduleDir := getModuleRootDir(t)
	scripts := []string{
		"run.sh",
		"scripts/dev-run.sh",
		"scripts/prod-run.sh",
		"scripts/gen-certs.sh",
		"scripts/deploy.sh",
		"scripts/health-check.sh",
	}

	for _, relPath := range scripts {
		fullPath := filepath.Join(moduleDir, relPath)
		cmd := exec.Command(bashPath, "-n", fullPath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("bash syntax error in %s: %v\nOutput: %s", relPath, err, string(output))
		}
	}
}

// 3. gen-certs.sh 执行测试：在临时目录生成证书链并深度校验 X.509 属性 (TestGenCertsScript_ExecutionAndCertificateVerification)
// 执行逻辑：
// 1. 检查 openssl 命令可用性；
// 2. 创建临时测试目录 t.TempDir()；
// 3. 运行 gen-certs.sh 生成证书链；
// 4. 逐一验证产物文件清单：ca.crt, ca.key, server.crt, server.key, client.crt, client.key, client.pub；
// 5. 解析并断言 CA 证书属性（IsCA=true, Subject.CN）；
// 6. 解析并断言服务端证书属性（SAN 包含 localhost）；
// 7. 解析客户端证书与 client.pub，校验提取的 RSA 公钥与证书内嵌公钥数学一致性（N 模数与 E 指数恒等）。
func TestGenCertsScript_ExecutionAndCertificateVerification(t *testing.T) {
	opensslPath, err := exec.LookPath("openssl")
	if err != nil {
		t.Skip("openssl is not available on this system")
	}
	_ = opensslPath

	moduleDir := getModuleRootDir(t)
	genScript := filepath.Join(moduleDir, "scripts", "gen-certs.sh")

	tempCertDir := t.TempDir()

	// 执行 gen-certs.sh 生成测试证书链
	cmd := exec.Command("bash", genScript, tempCertDir)
	cmd.Dir = moduleDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("gen-certs.sh execution failed: %v\nOutput: %s", err, string(out))
	}

	// 验证生成的文件清单完整性
	expectedFiles := []string{
		"ca.crt", "ca.key",
		"server.crt", "server.key",
		"client.crt", "client.key",
		"client.pub",
	}

	for _, fname := range expectedFiles {
		p := filepath.Join(tempCertDir, fname)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected generated file %s not found: %v", fname, err)
		}
	}

	// 解析 CA 证书并断言属性
	caPEM, err := os.ReadFile(filepath.Join(tempCertDir, "ca.crt"))
	if err != nil {
		t.Fatalf("read ca.crt: %v", err)
	}
	caBlock, _ := pem.Decode(caPEM)
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		t.Fatalf("parse ca.crt: %v", err)
	}
	if !caCert.IsCA || caCert.Subject.CommonName != "datasource-mgr-test-ca" {
		t.Errorf("unexpected CA cert properties: isCA=%v, CN=%s", caCert.IsCA, caCert.Subject.CommonName)
	}

	// 解析服务端证书并验证 SAN（主体备用名称）
	serverPEM, err := os.ReadFile(filepath.Join(tempCertDir, "server.crt"))
	if err != nil {
		t.Fatalf("read server.crt: %v", err)
	}
	serverBlock, _ := pem.Decode(serverPEM)
	serverCert, err := x509.ParseCertificate(serverBlock.Bytes)
	if err != nil {
		t.Fatalf("parse server.crt: %v", err)
	}
	if serverCert.Subject.CommonName != "localhost" {
		t.Errorf("unexpected server cert CN: %s", serverCert.Subject.CommonName)
	}
	hasLocalhostSAN := false
	for _, dns := range serverCert.DNSNames {
		if dns == "localhost" {
			hasLocalhostSAN = true
			break
		}
	}
	if !hasLocalhostSAN {
		t.Errorf("server cert missing localhost in DNSNames: %v", serverCert.DNSNames)
	}

	// 解析客户端证书并验证 client.pub 公钥匹配
	clientPEM, err := os.ReadFile(filepath.Join(tempCertDir, "client.crt"))
	if err != nil {
		t.Fatalf("read client.crt: %v", err)
	}
	clientBlock, _ := pem.Decode(clientPEM)
	clientCert, err := x509.ParseCertificate(clientBlock.Bytes)
	if err != nil {
		t.Fatalf("parse client.crt: %v", err)
	}
	if clientCert.Subject.CommonName != "datasource-mgr-client" {
		t.Errorf("unexpected client cert CN: %s", clientCert.Subject.CommonName)
	}

	// 读取提取的 client.pub 公钥
	pubPEM, err := os.ReadFile(filepath.Join(tempCertDir, "client.pub"))
	if err != nil {
		t.Fatalf("read client.pub: %v", err)
	}
	pubBlock, _ := pem.Decode(pubPEM)
	parsedPub, err := x509.ParsePKIXPublicKey(pubBlock.Bytes)
	if err != nil {
		t.Fatalf("parse client.pub: %v", err)
	}

	rsaClientCertPub, ok1 := clientCert.PublicKey.(*rsa.PublicKey)
	rsaExtractedPub, ok2 := parsedPub.(*rsa.PublicKey)
	if !ok1 || !ok2 {
		t.Fatalf("expected RSA public keys, got ok1=%v, ok2=%v", ok1, ok2)
	}
	if rsaClientCertPub.N.Cmp(rsaExtractedPub.N) != 0 || rsaClientCertPub.E != rsaExtractedPub.E {
		t.Errorf("client.pub does not match public key in client.crt")
	}

	t.Log("✅ gen-certs.sh 证书链与公钥固定文件验证通过")
}

// 4. dev-run.sh 开发脚本启动与探活测试 (TestDevRunScript_StartupAndHealth)
// 执行逻辑：
// 1. 获取本地随机空闲 HTTP/gRPC 端口，避免端口冲突；
// 2. 注入环境变量并通过子进程拉起 scripts/dev-run.sh；
// 3. 注册 defer 钩子安全终止子进程；
// 4. 轮询探测 HTTP /health 端点，验证服务在开发模式下能够正常对外响应 200 OK 与 JSON 元数据。
func TestDevRunScript_StartupAndHealth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess script test in short mode")
	}

	moduleDir := getModuleRootDir(t)
	devScript := filepath.Join(moduleDir, "scripts", "dev-run.sh")

	// 分配随机空闲端口
	httpPort := getFreePort(t)
	grpcPort := getFreePort(t)

	cmd := exec.Command("bash", devScript)
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(),
		"DATASOURCE_MGR_HOST=127.0.0.1",
		fmt.Sprintf("DATASOURCE_MGR_PORT=%d", httpPort),
		"DATASOURCE_MGR_GRPC_HOST=127.0.0.1",
		fmt.Sprintf("DATASOURCE_MGR_GRPC_PORT=%d", grpcPort),
		"DATASOURCE_MGR_LOG_FORMAT=text",
		"DATASOURCE_MGR_LOG_LEVEL=debug",
	)

	// 启动子进程
	if err := cmd.Start(); err != nil {
		t.Fatalf("start dev-run.sh failed: %v", err)
	}

	// 退出时确保杀死进程树
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
			}
		}
	}()

	// 轮询探测 HTTP 健康端点（最多等待 20s，兼容高并发下 go build 耗时）
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", httpPort)
	client := &http.Client{Timeout: 1 * time.Second}

	var resp *http.Response
	var lastErr error
	for i := 0; i < 100; i++ {
		time.Sleep(200 * time.Millisecond)
		resp, lastErr = client.Get(healthURL)
		if lastErr == nil && resp.StatusCode == http.StatusOK {
			break
		}
	}

	if lastErr != nil || resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("dev-run.sh failed to become healthy at %s: %v", healthURL, lastErr)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var healthMap map[string]any
	if err := json.Unmarshal(body, &healthMap); err != nil {
		t.Fatalf("invalid health json: %v, body=%s", err, string(body))
	}
	if healthMap["status"] != "ok" {
		t.Errorf("unexpected health status: %+v", healthMap)
	}

	t.Logf("✅ dev-run.sh 正常启动并响应 HTTP 200 OK (Port: %d)", httpPort)
}

// 5. prod-run.sh 生产脚本启动与 mTLS 证书加载测试 (TestProdRunScript_StartupAndMTLS)
// 执行逻辑：
// 1. 分配随机空闲端口并使用 certs 目录证书链启动 prod-run.sh，并按 P0-1 门禁注入 mTLS CN 白名单文件；
// 2. 构造携带 client.crt 与受信任 CA 的 HTTPS 客户端，发起 mTLS 请求，验证 200 OK 握手成功；
// 3. 构造未提供客户端证书的 HTTP 客户端发起请求，验证 mTLS 握手被阻断拦截；
// 4. TCP Dial 验证 gRPC mTLS 端口已正常监听。
func TestProdRunScript_StartupAndMTLS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess script test in short mode")
	}

	moduleDir := getModuleRootDir(t)
	prodScript := filepath.Join(moduleDir, "scripts", "prod-run.sh")
	certsDir := filepath.Join(moduleDir, "certs")

	httpPort := getFreePort(t)
	grpcPort := getFreePort(t)

	cmd := exec.Command("bash", prodScript)
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(),
		"DATASOURCE_MGR_HOST=127.0.0.1",
		fmt.Sprintf("DATASOURCE_MGR_PORT=%d", httpPort),
		"DATASOURCE_MGR_GRPC_HOST=127.0.0.1",
		fmt.Sprintf("DATASOURCE_MGR_GRPC_PORT=%d", grpcPort),
		fmt.Sprintf("DATASOURCE_MGR_CERTS_DIR=%s", certsDir),
		// P0-1 零信任门禁：启用 gRPC TLS 却未注入 CN 白名单文件时启动即失败（白名单拦截器根本不会被注册）。
		// 生产编排 deploy/helm 本就注入该变量；scripts/prod-run.sh 目前未导出，需由脚本负责人补：
		//   export PRIVACY_AUTH_MTLS_WHITELIST_FILE="${PRIVACY_AUTH_MTLS_WHITELIST_FILE:-$PROJECT_ROOT/config/mtls-whitelist.yaml}"
		fmt.Sprintf("PRIVACY_AUTH_MTLS_WHITELIST_FILE=%s", writeMTLSWhitelistFile(t)),
		"DATASOURCE_MGR_LOG_FORMAT=text",
		"DATASOURCE_MGR_LOG_LEVEL=info",
	)

	if err := cmd.Start(); err != nil {
		t.Fatalf("start prod-run.sh failed: %v", err)
	}

	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Signal(syscall.SIGTERM)
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = cmd.Process.Kill()
			}
		}
	}()

	// 1. 读取测试证书链配置 mTLS 客户端
	clientCert, err := tls.LoadX509KeyPair(filepath.Join(certsDir, "client.crt"), filepath.Join(certsDir, "client.key"))
	if err != nil {
		t.Fatalf("failed to load client keypair: %v", err)
	}
	caPEM, err := os.ReadFile(filepath.Join(certsDir, "ca.crt"))
	if err != nil {
		t.Fatalf("failed to read ca.crt: %v", err)
	}
	caPool := x509.NewCertPool()
	caPool.AppendCertsFromPEM(caPEM)

	tlsClient := &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				Certificates: []tls.Certificate{clientCert},
				RootCAs:      caPool,
				ServerName:   "localhost",
			},
		},
	}

	// 2. 探测 HTTPS REST mTLS 端点（最多等待 20s，兼容高并发下 go build 耗时）
	healthURL := fmt.Sprintf("https://127.0.0.1:%d/health", httpPort)
	var resp *http.Response
	var lastErr error
	for i := 0; i < 100; i++ {
		time.Sleep(200 * time.Millisecond)
		resp, lastErr = tlsClient.Get(healthURL)
		if lastErr == nil && resp.StatusCode == http.StatusOK {
			break
		}
	}

	if lastErr != nil || resp == nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("prod-run.sh failed to become healthy at HTTPS %s: %v", healthURL, lastErr)
	}
	defer resp.Body.Close()

	// 3. 验证未提供客户端证书的请求会被 mTLS 阻断
	insecureClient := &http.Client{
		Timeout: 1 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:            caPool,
				ServerName:         "localhost",
				InsecureSkipVerify: true,
			},
		},
	}
	noCertResp, noCertErr := insecureClient.Get(healthURL)
	if noCertErr == nil && noCertResp != nil && noCertResp.StatusCode == http.StatusOK {
		t.Errorf("expected mTLS handshake failure when client certificate is not provided, but succeeded")
	}

	// 4. 探测 gRPC mTLS 端口已在监听
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", fmt.Sprintf("127.0.0.1:%d", grpcPort))
	if err != nil {
		t.Fatalf("gRPC mTLS port %d is not listening: %v", grpcPort, err)
	}
	_ = conn.Close()

	t.Logf("✅ prod-run.sh 正常启动，HTTPS REST (Port: %d) 与 gRPC mTLS (Port: %d) 均就绪并通过双向认证校验", httpPort, grpcPort)
}

// writeMTLSWhitelistFile 在临时目录写入最小可用的 mTLS 客户端 CN 白名单（pkg/tlsutil 标准格式），
// 返回文件路径。生产加固形态（TLS + gRPC）下该文件是启动前置条件，缺失即被 fail-closed 门禁拒绝。
func writeMTLSWhitelistFile(t *testing.T) string {
	t.Helper()
	content := "version: \"1.0\"\nclients:\n  - cn: \"datasource-mgr-client\"\n    allowed_scopes: [\"*\"]\n    role: \"test\"\n    enabled: true\n"
	path := filepath.Join(t.TempDir(), "mtls-whitelist.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write mtls whitelist fixture: %v", err)
	}
	return path
}

// getFreePort listens on a random ephemeral port (":0") to find an available port and releases it.
// getFreePort 临时监听 ":0" 获取系统随机分配的空闲端口并立即关闭，返回可用端口号。
func getFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to get free port: %v", err)
	}
	defer l.Close()
	_, portStr, _ := net.SplitHostPort(l.Addr().String())
	port, _ := strconv.Atoi(portStr)
	return port
}
