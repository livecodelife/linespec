package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/calebcowen/linespec/pkg/docker"
	"github.com/calebcowen/linespec/pkg/dsl"
	"github.com/calebcowen/linespec/pkg/registry"
	"github.com/calebcowen/linespec/pkg/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
)

type Runner struct {
	orch     *docker.DockerOrchestrator
	registry *registry.MockRegistry
}

func NewRunner() (*Runner, error) {
	orch, err := docker.NewDockerOrchestrator()
	if err != nil {
		return nil, err
	}
	return &Runner{
		orch:     orch,
		registry: registry.NewMockRegistry(),
	}, nil
}

func (r *Runner) RunTest(ctx context.Context, specPath string) error {
	// 1. Load Spec
	tokens, err := dsl.LexFile(specPath)
	if err != nil {
		return err
	}
	parser := dsl.NewParser(tokens)
	spec, err := parser.Parse(specPath)
	if err != nil {
		return err
	}
	r.registry.Register(spec)

	// Pre-cleanup
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
	_ = r.orch.StopAndRemoveContainer(cleanupCtx, "db-"+spec.Name)
	_ = r.orch.StopAndRemoveContainer(cleanupCtx, "app-"+spec.Name)
	_ = r.orch.StopAndRemoveContainer(cleanupCtx, "proxy-db-"+spec.Name)
	_ = r.orch.StopAndRemoveContainer(cleanupCtx, "proxy-http-"+spec.Name)
	_ = r.orch.StopAndRemoveContainer(cleanupCtx, "kafka-"+spec.Name)
	_ = r.orch.RemoveNetwork(cleanupCtx, "linespec-net-"+spec.Name)
	cleanupCancel()

	// 2. Setup Network
	netName := "linespec-net-" + spec.Name
	_, err = r.orch.CreateNetwork(ctx, netName)
	if err != nil {
		_ = r.orch.RemoveNetwork(context.Background(), netName)
		_, err = r.orch.CreateNetwork(ctx, netName)
		if err != nil {
			return err
		}
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_ = r.orch.RemoveNetwork(c, netName)
	}()

	// 3. Start Database
	serviceDir := "user-service"
	appPort := "3001"
	if strings.Contains(specPath, "todo-linespecs") {
		serviceDir = "todo-api"
		appPort = "3000"
	}

	cwd, _ := os.Getwd()
	initSqlPath := filepath.Join(cwd, serviceDir, "init.sql")

	_, err = r.orch.StartContainer(ctx, &container.Config{
		Image: "mysql:8.4",
		Env:   []string{"MYSQL_ROOT_PASSWORD=rootpassword", "MYSQL_DATABASE=todo_api_development", "MYSQL_USER=todo_user", "MYSQL_PASSWORD=todo_password"},
	}, &container.HostConfig{
		Binds: []string{fmt.Sprintf("%s:/docker-entrypoint-initdb.d/init.sql", initSqlPath)},
		PortBindings: map[nat.Port][]nat.PortBinding{
			"3306/tcp": {{HostIP: "0.0.0.0", HostPort: "0"}},
		},
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{netName: {Aliases: []string{"real-db"}}},
	}, "db-"+spec.Name)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.orch.StopAndRemoveContainer(cleanupCtx, "db-"+spec.Name)
	}()

	fmt.Println("Waiting for DB to be ready...")
	if err := r.orch.WaitTCPInternal(ctx, netName, "real-db:3306", 60*time.Second); err != nil {
		return err
	}
	// MySQL is ready when TCP responds, no need for additional sleep

	// 4. Save Registry to File for Proxy Containers
	regFile := filepath.Join(cwd, "registry.json")
	_ = r.registry.SaveToFile(regFile)
	defer os.Remove(regFile)

	// 5. Start Proxy Containers

	// MySQL Proxy
	_, err = r.orch.StartContainer(ctx, &container.Config{
		Image: "linespec:latest",
		Cmd:   []string{"proxy", "mysql", "0.0.0.0:3306", "real-db:3306", "/app/project/registry.json"},
	}, &container.HostConfig{
		Binds: []string{cwd + ":/app/project"},
		PortBindings: map[nat.Port][]nat.PortBinding{
			"8081/tcp": {{HostIP: "0.0.0.0", HostPort: "0"}},
		},
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{netName: {Aliases: []string{"db"}}},
	}, "proxy-db-"+spec.Name)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.orch.StopAndRemoveContainer(cleanupCtx, "proxy-db-"+spec.Name)
	}()

	// HTTP Proxy
	_, err = r.orch.StartContainer(ctx, &container.Config{
		Image: "linespec:latest",
		Cmd:   []string{"proxy", "http", "0.0.0.0:80", "unused", "/app/project/registry.json"},
	}, &container.HostConfig{
		Binds: []string{cwd + ":/app/project"},
		PortBindings: map[nat.Port][]nat.PortBinding{
			"8081/tcp": {{HostIP: "0.0.0.0", HostPort: "0"}},
		},
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{netName: {Aliases: []string{"user-service.local"}}},
	}, "proxy-http-"+spec.Name)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.orch.StopAndRemoveContainer(cleanupCtx, "proxy-http-"+spec.Name)
	}()

	// Real Kafka broker (not mocked)
	_, err = r.orch.StartContainer(ctx, &container.Config{
		Image:    "confluentinc/cp-kafka:latest",
		Hostname: "kafka",
		Env: []string{
			"KAFKA_BROKER_ID=1",
			"KAFKA_LISTENER_SECURITY_PROTOCOL_MAP=PLAINTEXT:PLAINTEXT,PLAINTEXT_HOST:PLAINTEXT,CONTROLLER:PLAINTEXT",
			"KAFKA_ADVERTISED_LISTENERS=PLAINTEXT://kafka:29092,PLAINTEXT_HOST://localhost:9092",
			"KAFKA_OFFSETS_TOPIC_REPLICATION_FACTOR=1",
			"KAFKA_TRANSACTION_STATE_LOG_MIN_ISR=1",
			"KAFKA_TRANSACTION_STATE_LOG_REPLICATION_FACTOR=1",
			"KAFKA_GROUP_INITIAL_REBALANCE_DELAY_MS=0",
			"KAFKA_AUTO_CREATE_TOPICS_ENABLE=true",
			"KAFKA_PROCESS_ROLES=broker,controller",
			"KAFKA_NODE_ID=1",
			"KAFKA_CONTROLLER_QUORUM_VOTERS=1@kafka:29093",
			"KAFKA_LISTENERS=PLAINTEXT://kafka:29092,CONTROLLER://kafka:29093,PLAINTEXT_HOST://0.0.0.0:9092",
			"KAFKA_INTER_BROKER_LISTENER_NAME=PLAINTEXT",
			"KAFKA_CONTROLLER_LISTENER_NAMES=CONTROLLER",
			"CLUSTER_ID=linespec-cluster",
		},
	}, &container.HostConfig{
		PortBindings: map[nat.Port][]nat.PortBinding{
			"9092/tcp":  {{HostIP: "0.0.0.0", HostPort: "0"}},
			"29092/tcp": {{HostIP: "0.0.0.0", HostPort: "0"}},
		},
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{netName: {Aliases: []string{"kafka"}}},
	}, "kafka-"+spec.Name)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.orch.StopAndRemoveContainer(cleanupCtx, "kafka-"+spec.Name)
	}()

	// Inspect all proxies to get ports and IPs
	inspectDb, _ := r.orch.GetContainerInspect(ctx, "proxy-db-"+spec.Name)
	dbVerifyPort := ""
	if p, ok := inspectDb.NetworkSettings.Ports["8081/tcp"]; ok && len(p) > 0 {
		dbVerifyPort = p[0].HostPort
	}

	inspectHttp, _ := r.orch.GetContainerInspect(ctx, "proxy-http-"+spec.Name)
	httpVerifyPort := ""
	if p, ok := inspectHttp.NetworkSettings.Ports["8081/tcp"]; ok && len(p) > 0 {
		httpVerifyPort = p[0].HostPort
	}
	proxyHttpIP := ""
	if n, ok := inspectHttp.NetworkSettings.Networks[netName]; ok {
		proxyHttpIP = n.IPAddress
	}

	// Wait for services to be ready on the network
	fmt.Println("Waiting for proxies to be ready...")
	if err := r.orch.WaitTCPInternal(ctx, netName, "db:3306", 30*time.Second); err != nil {
		return fmt.Errorf("MySQL proxy not ready: %w", err)
	}
	if err := r.orch.WaitTCPInternal(ctx, netName, "user-service.local:80", 30*time.Second); err != nil {
		return fmt.Errorf("HTTP proxy not ready: %w", err)
	}
	if err := r.orch.WaitTCPInternal(ctx, netName, "kafka:29092", 30*time.Second); err != nil {
		return fmt.Errorf("Kafka not ready: %w", err)
	}

	// 6. Start SUT
	appEnv := []string{
		"DB_HOST=db",
		"DB_PORT=3306",
		"DB_USERNAME=todo_user",
		"DB_PASSWORD=todo_password",
		"RAILS_ENV=development",
		"KAFKA_BROKERS=kafka:29092",
		"KAFKA_TOPIC=todo-events",
		"USER_SERVICE_URL=http://" + proxyHttpIP + ":80/api/v1/users/auth",
	}

	extraHosts := []string{}
	if proxyHttpIP != "" {
		extraHosts = append(extraHosts, "user-service.local:"+proxyHttpIP)
	}

	// Safety: If IP still empty, use host-gateway as fallback to avoid crash
	if proxyHttpIP == "" {
		extraHosts = append(extraHosts, "user-service.local:host-gateway")
	}

	_, err = r.orch.StartContainer(ctx, &container.Config{
		Image: serviceDir + ":latest",
		Env:   appEnv,
		Cmd:   []string{"bash", "-c", "rm -f tmp/pids/server.pid && bundle exec rails db:migrate && bundle exec rails server -b 0.0.0.0 -p " + appPort},
	}, &container.HostConfig{
		ExtraHosts: extraHosts,
		PortBindings: map[nat.Port][]nat.PortBinding{
			nat.Port(appPort + "/tcp"): {{HostIP: "0.0.0.0", HostPort: "0"}},
		},
	}, &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{netName: {}},
	}, "app-"+spec.Name)
	if err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = r.orch.StopAndRemoveContainer(cleanupCtx, "app-"+spec.Name)
	}()

	inspectApp, _ := r.orch.GetContainerInspect(ctx, "app-"+spec.Name)
	hostPort := ""
	if p, ok := inspectApp.NetworkSettings.Ports[nat.Port(appPort+"/tcp")]; ok && len(p) > 0 {
		hostPort = p[0].HostPort
	}
	fmt.Printf("App started on host port: %s\n", hostPort)

	// 7. Wait for App
	fmt.Println("Waiting for App to be healthy...")
	healthURL := fmt.Sprintf("http://localhost:%s/up", hostPort)
	if err := r.orch.WaitHTTP(ctx, healthURL, 120*time.Second); err != nil {
		fmt.Println("❌ App failed to become healthy")
		logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer logCancel()
		_ = r.orch.StreamLogs(logCtx, "app-"+spec.Name, os.Stdout, os.Stderr)
		return err
	}
	fmt.Println("✅ App is healthy")

	// 8. Trigger Request
	fmt.Printf("🚀 Triggering RECEIVE: %s %s\n", spec.Receive.Method, spec.Receive.Path)
	resp, err := r.sendRequest(spec.Receive, spec.BaseDir, hostPort)
	if err != nil {
		fmt.Printf("❌ Trigger request failed: %v\n", err)
		return err
	}
	defer resp.Body.Close()
	fmt.Printf("✅ Received response: %d\n", resp.StatusCode)

	// 9. Verify Response
	if resp.StatusCode != spec.Respond.StatusCode {
		fmt.Printf("❌ Test failed with status %d. Fetching app logs...\n", resp.StatusCode)
		logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer logCancel()
		_ = r.orch.StreamLogs(logCtx, "app-"+spec.Name, os.Stdout, os.Stderr)
		return fmt.Errorf("expected status %d, got %d", spec.Respond.StatusCode, resp.StatusCode)
	}

	if spec.Respond.WithFile != "" {
		loader := &dsl.PayloadLoader{BaseDir: spec.BaseDir}
		expected, err := loader.Load(spec.Respond.WithFile)
		if err != nil {
			return fmt.Errorf("failed to load expected response payload: %v", err)
		}

		actualRaw, _ := io.ReadAll(resp.Body)
		var actual interface{}
		_ = json.Unmarshal(actualRaw, &actual)

		if err := r.comparePayloads(expected, actual, spec.Respond.Noise); err != nil {
			fmt.Printf("❌ Response body mismatch: %v\n", err)
			return err
		}
	}

	// 10. Final Registry Verification
	if dbVerifyPort != "" {
		r.collectHits("localhost:" + dbVerifyPort)
	}
	if httpVerifyPort != "" {
		r.collectHits("localhost:" + httpVerifyPort)
	}
	// Small delay for any remaining async operations
	time.Sleep(500 * time.Millisecond)

	if err := r.registry.VerifyAll(); err != nil {
		fmt.Printf("❌ Mock verification failed: %v\n", err)
		// Stream app logs for debugging
		logCtx, logCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer logCancel()
		fmt.Println("📋 Fetching app logs for debugging...")
		_ = r.orch.StreamLogs(logCtx, "app-"+spec.Name, os.Stdout, os.Stderr)
		return err
	}

	fmt.Println("✨ Test passed!")
	return nil
}

func (r *Runner) collectHits(addr string) {
	fmt.Printf("Proxy: Collecting hits from %s...\n", addr)
	for i := 0; i < 5; i++ {
		resp, err := http.Get("http://" + addr + "/verify")
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		defer resp.Body.Close()

		var hits map[string]int
		if err := json.NewDecoder(resp.Body).Decode(&hits); err != nil {
			return
		}
		r.registry.SetHits(hits)
		return
	}
}

func (r *Runner) sendRequest(receive types.ReceiveStatement, baseDir string, port string) (*http.Response, error) {
	url := "http://localhost:" + port + receive.Path
	var body io.Reader
	if receive.WithFile != "" {
		loader := &dsl.PayloadLoader{BaseDir: baseDir}
		payload, err := loader.Load(receive.WithFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load trigger payload: %v", err)
		}
		data, _ := json.Marshal(payload)
		body = strings.NewReader(string(data))
	}

	req, _ := http.NewRequest(receive.Method, url, body)
	if receive.WithFile != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range receive.Headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{}
	return client.Do(req)
}

func (r *Runner) comparePayloads(expected, actual interface{}, noise []string) error {
	noiseMap := make(map[string]bool)
	for _, n := range noise {
		noiseMap[n] = true
	}
	return r.compareRecursive(expected, actual, "body", noiseMap)
}

func (r *Runner) compareRecursive(exp, act interface{}, path string, noise map[string]bool) error {
	if noise[path] {
		return nil
	}

	switch e := exp.(type) {
	case map[string]interface{}:
		a, ok := act.(map[string]interface{})
		if !ok {
			return fmt.Errorf("at %s: expected object, got %T", path, act)
		}
		for k, v := range e {
			newPath := path + "." + k
			if err := r.compareRecursive(v, a[k], newPath, noise); err != nil {
				return err
			}
		}
	case []interface{}:
		a, ok := act.([]interface{})
		if !ok {
			return fmt.Errorf("at %s: expected array, got %T", path, act)
		}
		if len(e) != len(a) {
			return fmt.Errorf("at %s: expected array length %d, got %d", path, len(e), len(a))
		}
		for i := range e {
			newPath := fmt.Sprintf("%s[%d]", path, i)
			if err := r.compareRecursive(e[i], a[i], newPath, noise); err != nil {
				return err
			}
		}
	default:
		expStr := fmt.Sprintf("%v", exp)
		actStr := fmt.Sprintf("%v", act)
		if expStr != actStr {
			return fmt.Errorf("at %s: expected %v, got %v", path, exp, act)
		}
	}
	return nil
}
