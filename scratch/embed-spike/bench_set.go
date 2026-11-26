package main

// 检索评测集：**查询刻意避开目标条目的用词**。
//
// 为什么不能用真实库量：本机库里 83 条有 90% 是 e2e 测试垃圾
// （`PICKER-直粘-VERIFY-78628`、`live-refresh-e2e-123`、一堆 `![](memoimg:…)`），
// 拿它算召回等于在噪声上算指标。所以按这个用户真实的工作域造一套，规模小但每条都标注
// 了"该命中谁"，失败可以逐条看（数字不是我一个人说了算）。
//
// 设计约束（这是这套评测唯一有说服力的地方）：
//   · 查询必须是**人真的会这么搜**的话，不是条目里的关键词；
//   · 查询与目标条目尽量不共享 3 字子串 —— 共享的话 FTS 本来就能命中，
//     那样测的是"FTS 还行"，而不是"语义搜索补了什么"。实际重合度会在结果里打出来。

type evalCase struct {
	Text   string // 语料条目（剪贴板里的样子）
	Query  string // 用户会怎么搜它
	Domain string // 仅用于分组看结果
}

var evalSet = []evalCase{
	// --- 运维 / 命令行 ---
	{`docker run -d --name pg -e POSTGRES_PASSWORD=xxxx -p 5432:5432 postgres:16`, `本地起一个数据库容器`, "运维"},
	{`sudo systemctl restart nginx`, `重启网页服务器`, "运维"},
	{`ssh ubuntu@81.70.43.29`, `连到那台云主机`, "运维"},
	{`scp -r ./dist ubuntu@81.70.43.29:/var/www`, `把编译结果传到服务器上`, "运维"},
	{`location /api { proxy_pass http://127.0.0.1:8080; }`, `把请求转到内网端口`, "运维"},
	{`kubectl get pods -n production`, `看线上容器跑没跑起来`, "运维"},
	{`psql -h localhost -U postgres -d clipnest`, `进数据库命令行`, "运维"},
	{`journalctl -u clipnest -f`, `看服务日志`, "运维"},
	{`docker compose up -d --build`, `一键把整套服务拉起来`, "运维"},
	{`chmod +x ./scripts/package.ps1`, `给脚本加执行权限`, "运维"},
	{`find / -name "*.log" -mtime -1`, `找昨天改过的日志文件`, "运维"},
	{`tar -czf backup.tar.gz ./data`, `把目录打成压缩包`, "运维"},
	{`wsl --install -d Ubuntu`, `在 Windows 里装 Linux`, "运维"},

	// --- 开发 ---
	{`git rebase -i HEAD~3`, `把最近三次提交合并`, "开发"},
	{`go test ./internal/... -run TestDraft`, `跑单元测试`, "开发"},
	{`npm run build`, `打包前端`, "开发"},
	{`tsc --noEmit`, `只做类型检查不产出`, "开发"},
	{`sqlite3 clipnest.db "select count(*) from entries"`, `查表里有多少行`, "开发"},
	{`pip install onnxruntime`, `装一个跑模型的库`, "开发"},
	{`curl -X POST -H "Authorization: Bearer xxx"`, `用命令行发请求带认证`, "开发"},

	// --- 这个项目自己的领域 ---
	{`webdav 坚果云 同步失败 检查服务器地址是否一字不差`, `云端传不上去`, "ClipNest"},
	{`ed25519 签名验证 自动更新`, `怎么确认安装包没被人改过`, "ClipNest"},
	{`DWMWA_USE_IMMERSIVE_DARK_MODE 亚克力 深色材质`, `弹窗背景跟着主题变`, "ClipNest"},
	{`把 Vditor 换成原生编辑器，输入法要能打中文`, `中文输入法能不能用`, "ClipNest"},
	{`内存占用从 753MB 降到 150MB`, `程序占多少内存`, "ClipNest"},
	{`快捷键 Ctrl+Shift+N 唤出记一笔`, `怎么快速记录`, "ClipNest"},
	{`深色主题下文字对比度不够，调成 4.5:1`, `字看不清`, "ClipNest"},
	{`附件超过 96MB 就不同步了`, `大文件传不上去`, "ClipNest"},
	{`剪贴板历史只留最近 500 条`, `太多条了怎么删`, "ClipNest"},

	// --- 工作沟通 ---
	{`明天下午三点和产品对齐下版本需求`, `约个会讨论做什么功能`, "沟通"},
	{`本周完成清洗结果下架留档接口，下周开始批量恢复`, `写工作汇报`, "沟通"},
	{`会议室 302，投屏密码 1234`, `在哪儿开会`, "沟通"},
	{`请款 600 元，走运营预算`, `报销流程`, "沟通"},
	{`这张图里的报错是 CORS 跨域，要在服务端加响应头`, `截图里那个错误怎么解决`, "沟通"},

	// --- 日常 ---
	{`顺丰单号 SF1234567890，明天到`, `快递到哪了`, "日常"},
	{`海淀区中关村大街 1 号 A 座 1203`, `公司地址`, "日常"},
	{`记得买牛奶和鸡蛋`, `超市要买什么`, "日常"},
	{`https://pan.quark.cn/s/3aad6dfbb6b3`, `那个网盘链接`, "日常"},
	{`这本书讲的是分布式共识，Raft 那章写得最好`, `推荐的技术书`, "日常"},
}

// negCases 是"应该没有正确答案"的查询（语料里根本没这类东西）。
// 用来观察阈值的下限：如果连这些都排出 0.7 的相似度，阈值就没法定。
var negCases = []string{
	`明天天气怎么样`,
	`红烧肉的做法`,
	`股票该不该卖`,
}
