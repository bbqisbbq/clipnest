// Command snipdump prints the snippets in a ClipNest database (dev helper).
//
//	$env:APPDATA='<sandbox>'; go run ./scratch/snipdump
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/bbqisbbq/clipnest/internal/clip"
)

func main() {
	dir := filepath.Join(os.Getenv("APPDATA"), "ClipNest")
	store, err := clip.Open(dir)
	if err != nil {
		fmt.Printf("open store: %v\n", err)
		return
	}
	defer store.Close()

	all := store.AllSnippets()
	fmt.Printf("snippets: %d\n", len(all))
	for _, sp := range all {
		fmt.Printf("- id=%s kind=%s trigger=%q group=%q title=%q used=%d updated=%s\n    body=%q\n",
			sp.ID, sp.Kind, sp.Trigger, sp.Group, sp.Title, sp.UseCount, sp.UpdatedAt.Format("15:04:05"), sp.Body)
	}
}
