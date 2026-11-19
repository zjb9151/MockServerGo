# CSMock

单文件运行的本地 HTTP Mock 服务。规则存在程序同级的 SQLite 里，用浏览器配置，不需要额外安装数据库或运行时。

## 运行

需要 Go 1.21 或更高版本。纯 Go SQLite，编译时不用 CGO。

```powershell
go build -o csmock.exe .
.\csmock.exe
```

启动后：

- Mock 服务：<http://127.0.0.1:8080>
- 配置页面：<http://127.0.0.1:9090>
- 数据库：与 `csmock.exe` 同级的 `data\csmock.db`

常用参数：

```powershell
.\csmock.exe -mock 127.0.0.1:8080 -admin 127.0.0.1:9090 -data D:\mock-data
.\csmock.exe -rules examples\rules.example.yaml -watch
```

`-mock` 和 `-admin` 会覆盖配置页里保存的监听地址。不写这两个参数时，使用上次在页面里保存的地址。修改监听地址后需要重启。`-watch` 会每 2 秒检查规则文件，保存后按文件内容重新导入。

`go run .` 时，数据库会放在当前工作目录的 `data` 下，因为临时编译出来的程序不在项目目录里。

## 规则怎么匹配

一条请求会在已启用的规则里挑选一条：

1. 优先级高的优先
2. 写明了 GET/POST 等方法的，优先于 `*`
3. 路径里固定段更多的优先于带参数的
4. 匹配条件更多的优先

路径参数同时支持 `/user/:id` 和 `/user/{id}`。

匹配条件的操作有：等于、不等于、忽略大小写等于、包含、前缀、后缀、正则、大于、大于等于、小于、小于等于、字段存在、字段不存在。Body 按 JSON 点路径取值，例如 `user.name`、`items.0.id`；整个正文用 `$raw`。

响应正文和响应 Header 可以使用：

```text
{{params.id}} {{query.q}} {{header.X-Token}} {{body.user.name}}
{{state.user}} {{method}} {{path}} {{rawquery}} {{uuid}} {{now}}
```

## 页面上能做什么

- 新增、修改、启用、删除规则，并立刻生效
- 按 Query、Header、Body、场景状态返回不同内容
- 设置状态码、响应头、延迟
- 试匹配：不写日志、不改状态
- 查看请求日志，把某次响应保存成 Mock 规则
- 实时监控页会在请求到达时立刻显示输入和输出，控制台也会打印同样的内容
- 用状态变量模拟登录前后
- 单条规则转发到真实服务并录制响应；也可以设置未命中时的兜底代理
- 导入、导出 JSON / YAML

第一次启动会写入 `/hello`、`/user/:id`、`/login`、`/me` 几条示例。示例只在空数据库时写入。

```powershell
curl.exe http://127.0.0.1:8080/hello
curl.exe "http://127.0.0.1:8080/user/42?verbose=yes"
curl.exe --% -X POST http://127.0.0.1:8080/login -H "Content-Type: application/json" -d "{\"username\":\"demo\"}"
curl.exe http://127.0.0.1:8080/me
```

`--%` 让 PowerShell 把后面的引号原样交给 curl。登录成功后再请求 `/me`，会读到刚才写入的场景状态。
