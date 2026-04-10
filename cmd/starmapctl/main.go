package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	httptransport "github.com/chenwenlong-java/StarMap/internal/transport/http"
)

const (
	defaultControlServer  = "http://127.0.0.1:18080"
	defaultControlTimeout = 5 * time.Second
	adminTokenEnvKey      = "STARMAP_ADMIN_TOKEN"
)

type successEnvelope struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type controlClient struct {
	baseURL string
	token   string
	client  *http.Client
}

// main 是 starmapctl 的命令行入口。
//
// 本机运维前提：
// 1. starmapd 已经启动，并且配置了独立 admin listener，例如 127.0.0.1:18080。
// 2. admin listener 当前只允许 127.0.0.1 访问，因此这些命令需要在目标节点本机执行。
// 3. 调用时必须提供 admin token，可以通过 --token 或环境变量 STARMAP_ADMIN_TOKEN 注入。
//
// 本机运维示例：
//
//  1. 设置 tok
//  1. 设置 token。
//     PowerShell:
//     $env:STARMAP_ADMIN_TOKEN="your-admin-token"
//
//  2. 查询当前节点视角下的集群状态。
//     go run ./cmd/starmapctl status
//
//  3. 新增 learner 节点。
//     go run ./cmd/starmapctl member add-learner --node-id=4 --http-addr=127.0.0.1:8083 --grpc-addr=127.0.0.1:19093 --admin-addr=127.0.0.1:18083
//
//  4. 将 learner 提升为正式投票节点。
//     go run ./cmd/starmapctl member promote --node-id=4
//
//  5. 移除节点。
//     go run ./cmd/starmapctl member remove --node-id=4
//
//  6. 主动转移 Leader。
//     go run ./cmd/starmapctl leader transfer --target-node-id=2
func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "member":
		runMember(os.Args[2:])
	case "leader":
		runLeader(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

// runMember 处理成员管理命令。
func runMember(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "member subcommand required: add-learner | promote | remove")
		os.Exit(1)
	}

	switch args[0] {
	case "add-learner":
		runAddLearner(args[1:])
	case "promote":
		runPromoteLearner(args[1:])
	case "remove":
		runRemoveMember(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown member subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// runLeader 处理 Leader 控制命令。
func runLeader(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "leader subcommand required: transfer")
		os.Exit(1)
	}

	switch args[0] {
	case "transfer":
		runTransferLeader(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown leader subcommand: %s\n", args[0])
		os.Exit(1)
	}
}

// runStatus 查询当前节点视角下的集群状态。
func runStatus(args []string) {
	flags := flag.NewFlagSet("status", flag.ExitOnError)
	server := flags.String("server", defaultControlServer, "starmapd 控制面 HTTP 地址")
	token := flags.String("token", "", "admin HTTP 固定鉴权 token；留空时尝试读取环境变量 STARMAP_ADMIN_TOKEN")
	timeout := flags.Duration("timeout", defaultControlTimeout, "请求超时")
	flags.Parse(args)

	client, err := newControlClient(*server, resolveAdminToken(*token), *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create client failed: %v\n", err)
		os.Exit(1)
	}

	var status httptransport.ClusterStatusDTO
	if err := client.Get("/admin/v1/status", &status); err != nil {
		fmt.Fprintf(os.Stderr, "query status failed: %v\n", err)
		os.Exit(1)
	}

	output, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "marshal status failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(output))
}

func runAddLearner(args []string) {
	flags := flag.NewFlagSet("member add-learner", flag.ExitOnError)
	server := flags.String("server", defaultControlServer, "starmapd 控制面 HTTP 地址")
	token := flags.String("token", "", "admin HTTP 固定鉴权 token；留空时尝试读取环境变量 STARMAP_ADMIN_TOKEN")
	timeout := flags.Duration("timeout", defaultControlTimeout, "请求超时")
	nodeID := flags.Uint64("node-id", 0, "待加入节点 ID")
	httpAddr := flags.String("http-addr", "", "待加入节点的 HTTP 地址")
	grpcAddr := flags.String("grpc-addr", "", "待加入节点的 gRPC 地址")
	adminAddr := flags.String("admin-addr", "", "待加入节点的 admin HTTP 地址")
	flags.Parse(args)

	if *nodeID == 0 {
		fmt.Fprintln(os.Stderr, "--node-id is required")
		os.Exit(1)
	}

	client, err := newControlClient(*server, resolveAdminToken(*token), *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create client failed: %v\n", err)
		os.Exit(1)
	}

	if err := client.Post("/admin/v1/members/add-learner", httptransport.MemberChangeRequestDTO{
		NodeID:    *nodeID,
		HTTPAddr:  *httpAddr,
		GRPCAddr:  *grpcAddr,
		AdminAddr: *adminAddr,
	}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "add learner failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("learner add requested: nodeId=%d\n", *nodeID)
}

func runPromoteLearner(args []string) {
	flags := flag.NewFlagSet("member promote", flag.ExitOnError)
	server := flags.String("server", defaultControlServer, "starmapd 控制面 HTTP 地址")
	token := flags.String("token", "", "admin HTTP 固定鉴权 token；留空时尝试读取环境变量 STARMAP_ADMIN_TOKEN")
	timeout := flags.Duration("timeout", defaultControlTimeout, "请求超时")
	nodeID := flags.Uint64("node-id", 0, "待提升节点 ID")
	flags.Parse(args)

	if *nodeID == 0 {
		fmt.Fprintln(os.Stderr, "--node-id is required")
		os.Exit(1)
	}

	client, err := newControlClient(*server, resolveAdminToken(*token), *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create client failed: %v\n", err)
		os.Exit(1)
	}

	if err := client.Post("/admin/v1/members/promote", httptransport.MemberChangeRequestDTO{
		NodeID: *nodeID,
	}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "promote learner failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("learner promote requested: nodeId=%d\n", *nodeID)
}

func runRemoveMember(args []string) {
	flags := flag.NewFlagSet("member remove", flag.ExitOnError)
	server := flags.String("server", defaultControlServer, "starmapd 控制面 HTTP 地址")
	token := flags.String("token", "", "admin HTTP 固定鉴权 token；留空时尝试读取环境变量 STARMAP_ADMIN_TOKEN")
	timeout := flags.Duration("timeout", defaultControlTimeout, "请求超时")
	nodeID := flags.Uint64("node-id", 0, "待移除节点 ID")
	flags.Parse(args)

	if *nodeID == 0 {
		fmt.Fprintln(os.Stderr, "--node-id is required")
		os.Exit(1)
	}

	client, err := newControlClient(*server, resolveAdminToken(*token), *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create client failed: %v\n", err)
		os.Exit(1)
	}

	if err := client.Post("/admin/v1/members/remove", httptransport.MemberChangeRequestDTO{
		NodeID: *nodeID,
	}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "remove member failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("member remove requested: nodeId=%d\n", *nodeID)
}

func runTransferLeader(args []string) {
	flags := flag.NewFlagSet("leader transfer", flag.ExitOnError)
	server := flags.String("server", defaultControlServer, "starmapd 控制面 HTTP 地址")
	token := flags.String("token", "", "admin HTTP 固定鉴权 token；留空时尝试读取环境变量 STARMAP_ADMIN_TOKEN")
	timeout := flags.Duration("timeout", defaultControlTimeout, "请求超时")
	targetNodeID := flags.Uint64("target-node-id", 0, "目标 Leader 节点 ID")
	flags.Parse(args)

	if *targetNodeID == 0 {
		fmt.Fprintln(os.Stderr, "--target-node-id is required")
		os.Exit(1)
	}

	client, err := newControlClient(*server, resolveAdminToken(*token), *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create client failed: %v\n", err)
		os.Exit(1)
	}

	if err := client.Post("/admin/v1/leader/transfer", httptransport.LeaderTransferRequestDTO{
		TargetNodeID: *targetNodeID,
	}, nil); err != nil {
		fmt.Fprintf(os.Stderr, "transfer leader failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("leader transfer requested: targetNodeId=%d\n", *targetNodeID)
}

func newControlClient(server, token string, timeout time.Duration) (*controlClient, error) {
	baseURL, err := normalizeBaseURL(server)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("admin token is required, use --token or %s", adminTokenEnvKey)
	}

	return &controlClient{
		baseURL: baseURL,
		token:   token,
		client: &http.Client{
			Timeout: timeout,
		},
	}, nil
}

func (c *controlClient) Get(path string, out interface{}) error {
	return c.do(http.MethodGet, path, nil, out, true)
}

func (c *controlClient) Post(path string, body interface{}, out interface{}) error {
	return c.do(http.MethodPost, path, body, out, true)
}

func (c *controlClient) do(method, path string, body interface{}, out interface{}, allowRedirect bool) error {
	var payload []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		payload = data
	}

	request, err := http.NewRequest(method, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Authorization", "Bearer "+c.token)

	response, err := c.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	data, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}

	if response.StatusCode >= 400 {
		var failure httptransport.ErrorResponse
		if err := json.Unmarshal(data, &failure); err != nil {
			return fmt.Errorf("http status=%d body=%s", response.StatusCode, string(data))
		}
		if allowRedirect && failure.Code == "not_leader" && failure.LeaderAddr != "" {
			nextBaseURL, err := resolveLeaderBaseURL(c.baseURL, failure.LeaderAddr)
			if err != nil {
				return err
			}
			nextClient := &controlClient{
				baseURL: nextBaseURL,
				token:   c.token,
				client:  c.client,
			}
			return nextClient.do(method, path, body, out, false)
		}
		return fmt.Errorf("%s: %s", failure.Code, failure.Message)
	}

	var success successEnvelope
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, &success); err != nil {
		return err
	}
	if out != nil && len(success.Data) > 0 && string(success.Data) != "null" {
		if err := json.Unmarshal(success.Data, out); err != nil {
			return err
		}
	}

	return nil
}

func normalizeBaseURL(server string) (string, error) {
	server = strings.TrimSpace(server)
	if server == "" {
		return "", errors.New("server is required")
	}
	if !strings.Contains(server, "://") {
		server = "http://" + server
	}

	parsed, err := url.Parse(server)
	if err != nil {
		return "", err
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("invalid server address: %s", server)
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawPath = parsed.Path
	return strings.TrimRight(parsed.String(), "/"), nil
}

func resolveLeaderBaseURL(currentBaseURL, leaderAddr string) (string, error) {
	current, err := url.Parse(currentBaseURL)
	if err != nil {
		return "", err
	}

	leaderAddr = strings.TrimSpace(leaderAddr)
	if leaderAddr == "" {
		return "", errors.New("leader address is empty")
	}
	if strings.Contains(leaderAddr, "://") {
		return normalizeBaseURL(leaderAddr)
	}
	if strings.HasPrefix(leaderAddr, ":") {
		host := current.Hostname()
		if host == "" {
			host = "127.0.0.1"
		}
		leaderAddr = host + leaderAddr
	}
	if _, _, err := netSplitHostPort(leaderAddr); err != nil {
		return "", err
	}

	return normalizeBaseURL(current.Scheme + "://" + leaderAddr)
}

func netSplitHostPort(address string) (string, int, error) {
	parts := strings.Split(address, ":")
	if len(parts) < 2 {
		return "", 0, fmt.Errorf("invalid address: %s", address)
	}

	port, err := strconv.Atoi(parts[len(parts)-1])
	if err != nil {
		return "", 0, err
	}
	host := strings.Join(parts[:len(parts)-1], ":")
	if host == "" {
		host = "127.0.0.1"
	}

	return host, port, nil
}

func resolveAdminToken(flagToken string) string {
	flagToken = strings.TrimSpace(flagToken)
	if flagToken != "" {
		return flagToken
	}

	return strings.TrimSpace(os.Getenv(adminTokenEnvKey))
}

// printUsage 打印当前 CLI 的使用说明。
func printUsage() {
	fmt.Println("Usage:")
	fmt.Println("  starmapctl status [--server=http://127.0.0.1:18080] [--token=your-token]")
	fmt.Println("  starmapctl member add-learner --node-id=4 [--http-addr=127.0.0.1:8083] [--grpc-addr=127.0.0.1:19093] [--admin-addr=127.0.0.1:18083] [--server=http://127.0.0.1:18080] [--token=your-token]")
	fmt.Println("  starmapctl member promote --node-id=4 [--server=http://127.0.0.1:18080] [--token=your-token]")
	fmt.Println("  starmapctl member remove --node-id=4 [--server=http://127.0.0.1:18080] [--token=your-token]")
	fmt.Println("  starmapctl leader transfer --target-node-id=2 [--server=http://127.0.0.1:18080] [--token=your-token]")
	fmt.Println()
	fmt.Println("说明:")
	fmt.Println("  starmapctl 通过 starmapd 独立的 admin HTTP 控制面执行状态查询、成员变更和 Leader 转移。")
	fmt.Println("  admin listener 当前只允许 127.0.0.1 访问，因此 starmapctl 需要在目标节点本机执行。")
	fmt.Println("  所有 admin 请求都会附带 Authorization: Bearer <token>，token 可通过 --token 或 STARMAP_ADMIN_TOKEN 提供。")
	fmt.Println("  当请求打到 follower 且返回 not_leader 时，会自动跟随到 leaderAddr 重试一次。")
}
