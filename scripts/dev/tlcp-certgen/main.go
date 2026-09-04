// tlcp-certgen 生成 TLCP（国密双证书）开发测试证书链：SM2 根 CA + 服务端签名证书 + 服务端加密证书。
//
// 用法:
//
//	go run ./scripts/dev/tlcp-certgen [-dir config/certs/tlcp]
//
// 输出文件（-dir 指定目录，默认 config/certs/tlcp）:
//
//	ca.crt / ca.key          SM2 自签根 CA
//	server-sign.crt/.key     服务端签名证书（KeyUsageDigitalSignature）
//	server-enc.crt/.key      服务端加密证书（KeyUsageKeyAgreement|KeyUsageDataEncipherment，gmtls 握手强校验）
//
// 已存在的完整证书集会被跳过（幂等），可用 -force 强制重新生成。
// 这些证书仅供本地开发/演练，生产环境必须使用合规 CA 签发的证书。
package main

import (
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/tjfoc/gmsm/sm2"
	tjfoctx509 "github.com/tjfoc/gmsm/x509"
)

func main() {
	dir := flag.String("dir", "config/certs/tlcp", "证书输出目录")
	force := flag.Bool("force", false, "强制重新生成（覆盖已有证书）")
	cn := flag.String("cn", "localhost", "服务端证书 CommonName")
	validity := flag.Duration("validity", 3650*24*time.Hour, "证书有效期")
	flag.Parse()

	need := []string{"ca.crt", "ca.key", "server-sign.crt", "server-sign.key", "server-enc.crt", "server-enc.key"}
	allExist := true
	for _, name := range need {
		if _, err := os.Stat(filepath.Join(*dir, name)); err != nil {
			allExist = false
			break
		}
	}
	if allExist && !*force {
		fmt.Printf("TLCP 证书已存在于 %s，跳过生成（-force 可强制重建）\n", *dir)
		return
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		exitf("create dir: %v", err)
	}

	// ── 1. SM2 根 CA ──────────────────────────────────────────────
	caKey, err := sm2.GenerateKey(nil)
	if err != nil {
		exitf("generate SM2 CA key: %v", err)
	}
	caTmpl := &tjfoctx509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "privshield-tlcp-dev-ca", Country: []string{"CN"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(*validity),
		IsCA:                  true,
		KeyUsage:              tjfoctx509.KeyUsageCertSign | tjfoctx509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		// 显式声明 SM2-SM3 签名算法：缺省时 gmsm 自签 CA 在 Verify 中会出现 "SM2 verification failure"。
		SignatureAlgorithm: tjfoctx509.SM2WithSM3,
	}
	caDER, err := tjfoctx509.CreateCertificate(caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		exitf("create SM2 CA certificate: %v", err)
	}
	caCert, err := tjfoctx509.ParseCertificate(caDER)
	if err != nil {
		exitf("parse SM2 CA certificate: %v", err)
	}
	caKeyDER, err := tjfoctx509.MarshalSm2PrivateKey(caKey, nil)
	if err != nil {
		exitf("marshal SM2 CA key: %v", err)
	}

	// ── 2. 服务端签名 / 加密双证书 ────────────────────────────────
	issue := func(serial int64, commonName string, encipherment bool) (certDER, keyDER []byte) {
		srvKey, err := sm2.GenerateKey(nil)
		if err != nil {
			exitf("generate SM2 server key: %v", err)
		}
		keyUsage := tjfoctx509.KeyUsageDigitalSignature
		if encipherment {
			// TLCP 加密证书要求 KeyAgreement/DataEncipherment（gmtls 握手强校验）。
			keyUsage = tjfoctx509.KeyUsageKeyAgreement | tjfoctx509.KeyUsageDataEncipherment
		}
		tmpl := &tjfoctx509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: commonName, Country: []string{"CN"}},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(*validity),
			KeyUsage:     keyUsage,
			ExtKeyUsage:  []tjfoctx509.ExtKeyUsage{tjfoctx509.ExtKeyUsageServerAuth},
			DNSNames:     []string{"localhost"},
			IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
			// 显式声明 SM2-SM3 签名算法（同 CA，避免 Verify 失败）。
			SignatureAlgorithm: tjfoctx509.SM2WithSM3,
		}
		der, err := tjfoctx509.CreateCertificate(tmpl, caCert, &srvKey.PublicKey, caKey)
		if err != nil {
			exitf("create SM2 server certificate: %v", err)
		}
		derKey, err := tjfoctx509.MarshalSm2PrivateKey(srvKey, nil)
		if err != nil {
			exitf("marshal SM2 server key: %v", err)
		}
		return der, derKey
	}

	signDER, signKeyDER := issue(2, *cn, false)
	encDER, encKeyDER := issue(3, *cn+"-enc", true)

	// ── 3. 落盘 ──────────────────────────────────────────────────
	files := []struct {
		name string
		typ  string
		der  []byte
	}{
		{"ca.crt", "CERTIFICATE", caDER},
		{"ca.key", "PRIVATE KEY", caKeyDER},
		{"server-sign.crt", "CERTIFICATE", signDER},
		{"server-sign.key", "PRIVATE KEY", signKeyDER},
		{"server-enc.crt", "CERTIFICATE", encDER},
		{"server-enc.key", "PRIVATE KEY", encKeyDER},
	}
	for _, f := range files {
		if err := writePEM(filepath.Join(*dir, f.name), f.typ, f.der); err != nil {
			exitf("write %s: %v", f.name, err)
		}
	}
	fmt.Printf("TLCP 开发证书已生成: %s\n", *dir)
}

func writePEM(path, typ string, der []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: typ, Bytes: der})
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "tlcp-certgen: "+format+"\n", args...)
	os.Exit(1)
}
