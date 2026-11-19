package clip

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// 更新包的信任根。
//
// 私钥在 build/signing/ed25519.key（已 gitignore），签名工具是
// scratch/signinstaller（keygen / sign / verify）。发布流程：给安装包签名 →
// 把 <安装包>.sig 一起传到 GitHub Release。
//
// ⚠️ 换掉下面这一行等于换信任根：老版本内置的是旧公钥，用新私钥签的包在老版本上
// 验签会失败（等于把所有人的自动更新掐断）。真要换，得先发一版"同时信任新旧两把
// 公钥"的过渡版本。
const updatePubKeyB64 = "5dVpRqBZgPMNE4T4Umu/lpEYOsSsoZDwcdTEpFTg80w="

// updatePubKey 是变量而不是常量，测试要注入自己的密钥对。
var updatePubKey = mustDecodeUpdatePubKey(updatePubKeyB64)

func mustDecodeUpdatePubKey(b64 string) ed25519.PublicKey {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) != ed25519.PublicKeySize {
		// 内置公钥坏了等于所有更新都装不上，这种事必须在第一次检查更新时就炸出来，
		// 而不是安静地"永远没有新版本"。
		panic(fmt.Sprintf("内置更新公钥无效（期望 %d 字节 base64）: %v", ed25519.PublicKeySize, err))
	}
	return ed25519.PublicKey(raw)
}

// resolveSigAsset 在同一个 release 里找安装包的 detached 签名（<安装包名>.sig）。
func resolveSigAsset(assets []ghAsset, installer string) *ghAsset {
	want := strings.ToLower(installer) + ".sig"
	for i := range assets {
		if strings.ToLower(assets[i].Name) == want {
			return &assets[i]
		}
	}
	return nil
}

const (
	maxSigBytes  = 4 << 10
	sigAssetType = "application/octet-stream"
)

// verifySignatureText 校验"文件字节 + base64 签名"，纯函数，方便测试。
// fail closed：签名缺失、编码不对、长度不对、验不过，一律 error。
func verifySignatureText(path, sigB64 string) error {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(sigB64))
	if err != nil {
		return fmt.Errorf("签名不是合法 base64: %w", err)
	}
	if len(raw) != ed25519.SignatureSize {
		return fmt.Errorf("签名长度异常（%d 字节，期望 %d）", len(raw), ed25519.SignatureSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("读安装包失败: %w", err)
	}
	if !ed25519.Verify(updatePubKey, data, raw) {
		return errors.New("安装包签名校验未通过（可能被替换或下载损坏），已中止安装")
	}
	return nil
}

// verifyUpdateSignature 取回 .sig 资产并校验本地安装包。
func verifyUpdateSignature(path string, sig *ghAsset) error {
	text, err := fetchSmallText(sig.DownloadURL)
	if err != nil {
		return fmt.Errorf("取签名文件失败: %w", err)
	}
	return verifySignatureText(path, text)
}

// fetchSmallText 拉一个很小的文本资源（签名只有 88 个 base64 字符）。
func fetchSmallText(url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), updateHTTPTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ClipNest/"+Version)

	resp, err := (&http.Client{Timeout: updateHTTPTimeout}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSigBytes))
	if err != nil {
		return "", err
	}
	return string(body), nil
}
