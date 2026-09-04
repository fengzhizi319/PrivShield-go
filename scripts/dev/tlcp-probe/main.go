// tlcp-probe 是 TLCP（国密）通道的 HTTP 探活工具：curl 无法讲 TLCP，故用 gmtls 客户端
// 完成国密握手后发送 HTTP GET，打印响应状态码。退出码 0 = 成功。
//
// 用法:
//
//	go run ./scripts/dev/tlcp-probe -url https://127.0.0.1:8079/health -ca config/certs/tlcp/ca.crt
//	go run ./scripts/dev/tlcp-probe -url https://127.0.0.1:8079/health -insecure-skip-verify
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/tjfoc/gmsm/gmtls"
	tjfoctx509 "github.com/tjfoc/gmsm/x509"
)

func main() {
	target := flag.String("url", "https://127.0.0.1:8079/health", "TLCP 探活地址（https:// 形式，内部走国密握手）")
	caFile := flag.String("ca", "", "SM2 根 CA PEM 路径（为空仅当 -insecure-skip-verify）")
	insecure := flag.Bool("insecure-skip-verify", false, "跳过服务端证书校验（仅开发/演练）")
	timeout := flag.Duration("timeout", 5*time.Second, "整体超时")
	flag.Parse()

	cfg := &gmtls.Config{
		GMSupport:          &gmtls.GMSupport{WorkMode: gmtls.ModeGMSSLOnly},
		InsecureSkipVerify: *insecure, //nolint:gosec // 开发探活工具，显式开关
	}
	if *caFile != "" {
		caPEM, err := os.ReadFile(*caFile)
		if err != nil {
			exitf("read CA file: %v", err)
		}
		pool := tjfoctx509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			exitf("parse CA pem %s failed", *caFile)
		}
		cfg.RootCAs = pool
	}

	u, err := url.Parse(*target)
	if err != nil || u.Hostname() == "" {
		exitf("cannot resolve host from url %q", *target)
	}
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "443"
	}
	// gmtls.Client 需要 ServerName；从目标地址取 host 部分兜底，保证证书 SAN 校验可用。
	if !cfg.InsecureSkipVerify && cfg.ServerName == "" {
		cfg.ServerName = host
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), *timeout)
	if err != nil {
		exitf("dial %s: %v", net.JoinHostPort(host, port), err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(*timeout))

	gmConn := gmtls.Client(conn, cfg)
	if err := gmConn.Handshake(); err != nil {
		exitf("TLCP handshake with %s: %v", *target, err)
	}

	req, err := http.NewRequest(http.MethodGet, *target, nil)
	if err != nil {
		exitf("build request: %v", err)
	}
	if err := req.Write(gmConn); err != nil {
		exitf("send request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(gmConn), req)
	if err != nil {
		exitf("read response: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	fmt.Printf("tlcp-probe %s -> %s\n", *target, resp.Status)
	if resp.StatusCode/100 != 2 {
		os.Exit(1)
	}
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "tlcp-probe: "+format+"\n", args...)
	os.Exit(1)
}
