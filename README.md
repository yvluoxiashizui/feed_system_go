# feedsystem

基于 Go + Gin + Redis + MySQL 的短视频 Feed 流系统。参考 [LeoninCS/feedsystem_video_go](https://github.com/LeoninCS/feedsystem_video_go) 实现核心闭环：用户注册登录、视频发布、Feed 流浏览、点赞、评论、关注、热榜。

## 功能

| 模块 | 功能 |
|------|------|
| 用户 | 注册、登录、JWT 签发与鉴权、按 ID 查用户 |
| 视频 | 发布视频（需登录）、Feed 流列表（游标分页 + 热点窗口缓存）、视频详情 |
| 点赞 | 点赞与取消点赞（复合唯一索引防重复）、写记录与计数同事务、是否已赞、我赞过的列表、热度累计 |
| 评论 | 发表评论（计入热度）、评论列表 |
| 关注 | 关注、取关、判断是否已关注、关注流（推拉结合：小号写粉丝时间线，大 V 读时聚合） |
| 热榜 | 点赞与评论按分钟分桶聚合最近一小时热度，快照缓存 30 秒，支持 offset/limit 分页 |
| 点赞榜 | 按点赞数排序，复合游标分页，（点赞数, id）联合索引 |
| 中间件 | 请求日志、Auth JWT 鉴权、令牌桶限流（Redis + Lua，注册/登录/发布） |

## 技术栈

| 分类 | 技术 |
|------|------|
| 语言 | Go |
| Web 框架 | Gin |
| 数据库 | MySQL + GORM |
| 缓存 | Redis（go-redis） |
| 消息队列 | RabbitMQ（amqp091-go），用于写扩散异步化 |
| 认证 | JWT（golang-jwt）+ bcrypt |
| 部署 | Docker Compose（MySQL / Redis / RabbitMQ / API / Worker） |

## Docker Compose 一键启动

```bash
docker compose up -d --build
```

启动五个容器：MySQL、Redis、RabbitMQ、API、Worker。API 在 `http://localhost:8080`，RabbitMQ 管理台在 `http://localhost:15672`（`feed` / `feed123`）。

## 本地开发

```bash
# 1. 建库（数据库连接配置在 bootstrap，默认本地开发值）
mysql -u root -e "CREATE DATABASE feed CHARACTER SET utf8mb4;"
mysql -u root < mysql-setup-feed.sql   # 建库建用户（可重复执行）

# 2. 启动 Redis 与 RabbitMQ
service redis-server start
service rabbitmq-server start

# 3. 启动 API 与 Worker（两个进程）
go run ./cmd/api
go run ./cmd/worker

# 4. 演示页（浏览器打开 http://localhost:8080/）
#    可注册登录、发布视频、点赞、切换最新/关注/热榜/点赞榜四个列表、游标翻页
```

常用环境变量：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `FEED_HTTP_ADDR` | `:8080` | 监听地址 |
| `FEED_DB_USER` / `FEED_DB_PASSWORD` / `FEED_DB_ADDR` / `FEED_DB_NAME` | 本地开发默认值 | MySQL 连接 |
| `FEED_REDIS_ADDR` | `localhost:6379` | Redis 地址 |
| `FEED_MQ_ADDR` | `amqp://guest:guest@127.0.0.1:5672/` | RabbitMQ 地址 |
| `FEED_MQ_DISABLED` | 未设置 | 设为 `1` 时写扩散退回同步 |
| `FEED_FANOUT_THRESHOLD` | `1000` | 粉丝数超过该值的作者走拉取 |
| `FEED_CACHE_DISABLED` | 未设置 | 设为 `1` 时关闭 Feed 缓存 |
| `FEED_ACCESS_LOG` | 未设置 | 设为 `0` 时关闭访问日志 |
| `FEED_UPLOAD_DIR` | `uploads` | 本地磁盘存储目录（未配对象存储时生效） |
| `FEED_S3_ENDPOINT` | 空 | S3 兼容地址，如 `http://127.0.0.1:9000`；配了就改用对象存储 |
| `FEED_S3_ACCESS_KEY` | 空 | 对象存储的 access key |
| `FEED_S3_SECRET_KEY` | 空 | 对象存储的 secret key |
| `FEED_S3_BUCKET` | `feed` | 桶名，不存在会自动创建并设为只读公开 |
| `FEED_S3_PUBLIC_URL` | 与 endpoint 相同 | 对外访问前缀，走 CDN 或反向代理时填它 |

## 接口清单

### 用户
| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| POST | `/register` | 否 | 注册（用户名只能字母数字，密码 bcrypt 加密） |
| POST | `/login` | 否 | 登录，返回 JWT token |
| GET | `/user?id=1` | 否 | 按 ID 查用户 |

### 视频
| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| POST | `/video/upload` | JWT | 小文件直传（≤100MB，mp4/webm/mov），返回可直接放进 `play_url` 的 `/media/...` 地址 |
| POST | `/video/upload/chunk` | JWT | 上传一个分片（≤5MB），参数 `upload_id`（文件摘要）、`index`、`total` |
| GET | `/video/upload/status?upload_id=` | JWT | 查询已传分片序号，用于断点续传 |
| POST | `/video/upload/complete` | JWT | 合并分片，幂等；分片不全时返回 400 |
| POST | `/video/publish` | JWT | 发布视频（标题 ≤100 字，播放地址 ≤500 字节）；可用 `Idempotency-Key` 头做重复提交幂等 |
| GET | `/videos?limit=&cursor=` | 可选 | Feed 流列表（游标分页，Redis 缓存最近 500 条 id 窗口，返回 `next_cursor`；带 JWT 时每条带 `is_liked`） |
| GET | `/video/detail?id=` | 可选 | 视频详情（带 JWT 时返回 `is_liked`） |

### 点赞
| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| POST | `/video/like` | JWT | 点赞（重复返回 400），写记录与计数在同一事务，同步累计热度 |
| POST | `/video/unlike` | JWT | 取消点赞（未点赞返回 400），计数回退且不会减成负数，热度归零时移出热榜 |
| GET | `/video/isLiked?video_id=` | JWT | 是否已点赞 |
| GET | `/video/liked?limit=&cursor=` | JWT | 我赞过的视频（游标为点赞记录 id） |

### 评论
| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| POST | `/comment/publish` | JWT | 发表评论（≤500 字，视频需存在） |
| GET | `/comment/list?video_id=&limit=&cursor=` | 否 | 视频的评论列表（游标分页，游标为评论 id） |

### 关注
| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| POST | `/social/follow` | JWT | 关注（重复返回 400） |
| POST | `/social/unfollow` | JWT | 取关（未关注返回 400） |
| GET | `/social/isFollowing?vlogger_id=` | JWT | 是否已关注 |
| GET | `/feed/following?limit=&cursor=` | JWT | 关注流（关注博主的最新视频，游标分页） |

### Feed / 热榜
| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| GET | `/feed/hot?limit=&offset=` | 否 | 视频热榜（分钟分桶聚合最近一小时，深度上限 100） |
| GET | `/feed/likes-rank?limit=&cursor=` | 否 | 点赞榜（按点赞数排序，复合游标形如 `20_111653`） |

### 测试与运维
| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| GET | `/auth-test` | JWT | 测试 Auth 中间件 |
| GET | `/healthz` | 否 | 健康检查（MySQL 不可用返回 503，Redis 不可用返回降级） |

## 性能压测

使用 [k6](https://k6.io/) 做本地混合场景压测（50 并发 / 5000 次迭代，50% Feed 列表、20% 视频详情、30% 发评论）。环境、负载模型与局限见 [docs/performance-testing.md](docs/performance-testing.md)。

| 方案 | RPS | P95 延迟 | 错误率 |
|------|-----|---------|--------|
| 无缓存（直接查 MySQL） | ~7400 | ~16.2 ms | 0% |
| **两级缓存（时间线 + 视频对象）** | **~7900** | **~16.2 ms** | 0% |

> 数字为三轮中位数。缓存带来的吞吐提升约 6%：被缓存的查询本身是主键范围扫描、约 0.2 ms，一次请求的大头是序列化与网络；聚合 P95 由评论写路径（约 19 ms）决定，读缓存对这条路径没有作用。

```bash
# 压测命令（k6）
k6 run loadtest.js

# 对照：关闭缓存
FEED_CACHE_DISABLED=1 go run ./cmd/api
k6 run loadtest.js
```

## 目录结构

```
feed/
├── cmd/
│   ├── api/main.go      # API 进程：连接依赖、建表、注册路由、投递扇出消息
│   └── worker/main.go   # Worker 进程：消费扇出消息，断开后自动重连
├── bootstrap/           # 两个进程共用的初始化：数据库连接池、Redis 客户端
├── models/              # 数据模型（entity：结构体 ↔ 数据库表）
│   ├── user.go / video.go / like.go / follow.go / comment.go
├── repos/               # 数据访问层（repository）：所有 MySQL 操作
│   ├── db.go / user_repo.go / video_repo.go / like_repo.go / follow_repo.go / comment_repo.go
├── services/            # 业务逻辑层（service）：业务编排 + Redis 缓存 + 扇出
│   ├── redis.go / user_service.go / video_service.go / follow_service.go / comment_service.go
│   ├── ratelimit.go     # 令牌桶限流（Lua 脚本保证取令牌与补令牌原子）
│   ├── hot_rank.go      # 热榜滑动窗口：分钟分桶、聚合快照、分页
│   ├── timeline.go      # 关注时间线：写扩散、回填、取关清理
│   └── mq.go            # 写扩散的消息投递与消费
├── handlers/            # 接口层（handler）：收请求、调 service、返回 JSON + 中间件
│   ├── user.go / video.go / follow.go / comment.go
│   ├── auth.go          # JWT 鉴权中间件
│   └── logger.go        # 日志中间件
├── preview.html         # 演示页：登录、上传发布、点赞、四种列表与游标翻页
├── uploads/             # 上传的视频文件与分片（已 gitignore），由 /media 路径提供访问
├── Dockerfile           # 多阶段构建，产出 api 与 worker 两个二进制
└── docker-compose.yml   # 五个服务一键启动
```

## 待办

- [x] 缓存穿透 / 击穿 / 雪崩防护
- [x] RabbitMQ 异步（写扩散）
- [ ] 消息消费失败监控与重放
