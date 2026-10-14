// Command safemedia 是 go-safemedia 的命令行入口。
package main

import (
	"os"

	"github.com/cc14514/go-safemedia/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
