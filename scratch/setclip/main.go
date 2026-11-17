//go:build windows

// setclip is a developer helper: it puts RTF or an arbitrary custom clipboard
// format on the clipboard so the capture pipeline can be exercised by hand.
//
//	go run ./scratch/setclip -rtf "{\rtf1\ansi Hello \b ClipNest\b0}"
//	go run ./scratch/setclip -custom MyFormat -payload deadbeef
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	rtf := flag.String("rtf", "", "RTF document to place on the clipboard")
	customName := flag.String("custom", "", "name of a custom clipboard format")
	payload := flag.String("payload", "", "hex payload for -custom")
	flag.Parse()

	switch {
	case *rtf != "":
		if err := clip.WriteRTFToClipboard(*rtf, ""); err != nil {
			fmt.Fprintln(os.Stderr, "rtf:", err)
			os.Exit(1)
		}
		fmt.Println("rtf placed on clipboard")
	case *customName != "":
		data, err := hex.DecodeString(*payload)
		if err != nil {
			fmt.Fprintln(os.Stderr, "payload:", err)
			os.Exit(1)
		}
		if err := clip.WriteRawToClipboard(0, *customName, data); err != nil {
			fmt.Fprintln(os.Stderr, "custom:", err)
			os.Exit(1)
		}
		fmt.Printf("custom format %q placed on clipboard (%d bytes)\n", *customName, len(data))
	default:
		flag.Usage()
		os.Exit(2)
	}
}
