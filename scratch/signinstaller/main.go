// Command signinstaller 是发布流程里给安装包签名 / 验签的小工具（开发用，不进产物）。
//
//	go run ./scratch/signinstaller keygen            # 生成密钥对，写 build/signing/ed25519.key
//	go run ./scratch/signinstaller pubkey            # 打印公钥（base64，粘进 update_sign.go）
//	go run ./scratch/signinstaller sign <file>       # 生成 <file>.sig
//	go run ./scratch/signinstaller verify <file> <sig>
//
// 签名对象是**文件字节**（detached signature），签名值以 base64 单行写进 .sig。
// 用 ed25519：私钥 32 字节种子，公钥 32 字节，签名 64 字节，都是定长，不需要参数协商。
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

const (
	keyDir  = "build/signing"
	keyFile = "ed25519.key"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "keygen":
		keygen()
	case "pubkey":
		pubkey()
	case "sign":
		if len(os.Args) < 3 {
			usage()
		}
		sign(os.Args[2])
	case "verify":
		if len(os.Args) < 4 {
			usage()
		}
		verify(os.Args[2], os.Args[3])
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "用法: signinstaller keygen | pubkey | sign <file> | verify <file> <sig>")
	os.Exit(2)
}

// keygen 只在没有私钥时生成（密钥一旦换了，已发布版本的更新会立刻全挂，
// 所以这里绝不覆盖既有文件）。
func keygen() {
	path := filepath.Join(keyDir, keyFile)
	if _, err := os.Stat(path); err == nil {
		fmt.Printf("私钥已存在，未覆盖: %s\n", path)
		return
	}
	if err := os.MkdirAll(keyDir, 0o700); err != nil {
		fatal("创建目录失败: %v", err)
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fatal("生成密钥失败: %v", err)
	}
	seed := priv.Seed()
	if err := os.WriteFile(path, []byte(base64.StdEncoding.EncodeToString(seed)), 0o600); err != nil {
		fatal("写私钥失败: %v", err)
	}
	fmt.Printf("私钥: %s（build/signing/ 已 gitignore，别提交、别外传）\n", path)
	fmt.Printf("公钥: %s\n", base64.StdEncoding.EncodeToString(pub))
	fmt.Println("把上面这行公钥粘进 internal/clip/update_sign.go 的 updatePubKeyB64。")
}

func pubkey() {
	fmt.Println(base64.StdEncoding.EncodeToString(privateKey().Public().(ed25519.PublicKey)))
}

func sign(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal("读文件失败: %v", err)
	}
	sig := ed25519.Sign(privateKey(), data)
	sigPath := path + ".sig"
	if err := os.WriteFile(sigPath, []byte(base64.StdEncoding.EncodeToString(sig)), 0o644); err != nil {
		fatal("写签名失败: %v", err)
	}
	fmt.Printf("已签名: %s\n  -> %s（%d 字节签名，载荷 %d 字节）\n", path, sigPath, len(sig), len(data))
}

func verify(path, sigPath string) {
	data, err := os.ReadFile(path)
	if err != nil {
		fatal("读文件失败: %v", err)
	}
	raw, err := os.ReadFile(sigPath)
	if err != nil {
		fatal("读签名失败: %v", err)
	}
	sig, err := base64.StdEncoding.DecodeString(string(trimSpace(raw)))
	if err != nil {
		fatal("签名不是合法 base64: %v", err)
	}
	if !ed25519.Verify(privateKey().Public().(ed25519.PublicKey), data, sig) {
		fatal("签名不匹配")
	}
	fmt.Println("签名有效")
}

func privateKey() ed25519.PrivateKey {
	raw, err := os.ReadFile(filepath.Join(keyDir, keyFile))
	if err != nil {
		fatal("读私钥失败（先跑 keygen）: %v", err)
	}
	seed, err := base64.StdEncoding.DecodeString(string(trimSpace(raw)))
	if err != nil || len(seed) != ed25519.SeedSize {
		fatal("私钥格式不对（期望 %d 字节种子的 base64）", ed25519.SeedSize)
	}
	return ed25519.NewKeyFromSeed(seed)
}

func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\n' || b[start] == '\r' || b[start] == '\t') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\n' || b[end-1] == '\r' || b[end-1] == '\t') {
		end--
	}
	return b[start:end]
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
