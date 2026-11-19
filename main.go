package main

import (
	"flag"
	"log"
	"os"

	"csmock/internal/app"
)

func main() {
	log.SetFlags(log.LstdFlags)
	mockAddr := flag.String("mock", "0.0.0.0:8080", "Mock 服务监听地址")
	adminAddr := flag.String("admin", "127.0.0.1:9090", "Web 配置页监听地址")
	dataDir := flag.String("data", "", "数据目录，默认在程序同级的 data 目录")
	rules := flag.String("rules", "", "启动时导入的 JSON 或 YAML 规则文件")
	watch := flag.Bool("watch", false, "监视 -rules 文件，保存后自动重新导入")
	flag.Parse()

	explicit := map[string]bool{}
	flag.Visit(func(f *flag.Flag) {
		explicit[f.Name] = true
	})

	err := app.Run(app.Config{
		MockAddr:  *mockAddr,
		AdminAddr: *adminAddr,
		DataDir:   *dataDir,
		RulesFile: *rules,
		Watch:     *watch,
		MockSet:   explicit["mock"],
		AdminSet:  explicit["admin"],
	})
	if err != nil {
		log.Fatal(err)
	}
	os.Exit(0)
}
