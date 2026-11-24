package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// The local TCP forwarder disconnects only these two test gateways from their
// actual shared Redis. It is a fault injector, never a replacement cache.
type gatewayCacheProxy struct {
	listener    net.Listener
	mu          sync.Mutex
	blocked     bool
	connections map[net.Conn]bool
}

func startGatewayCacheProxy() *gatewayCacheProxy {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	must(err)
	p := &gatewayCacheProxy{listener: listener, connections: map[net.Conn]bool{}}
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			go p.forward(client)
		}
	}()
	return p
}

func (p *gatewayCacheProxy) forward(client net.Conn) {
	defer client.Close()
	p.mu.Lock()
	if p.blocked {
		p.mu.Unlock()
		return
	}
	p.connections[client] = true
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.connections, client); p.mu.Unlock() }()
	upstream, err := net.DialTimeout("tcp", "redis:6379", time.Second)
	if err != nil {
		return
	}
	defer upstream.Close()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close(); close(done) }()
	_, _ = io.Copy(client, upstream)
	_ = client.Close()
	<-done
}

func (p *gatewayCacheProxy) block(value bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.blocked = value
	if value {
		for conn := range p.connections {
			_ = conn.Close()
		}
	}
}

func runGatewayCacheFailure(proxy *gatewayCacheProxy) {
	user, token, gift := realtimeFixtureAccount("cachefault", 600, 0)
	request("18371", "/v1/chat/completions", token.Key, `{"model":"gpt-4o","messages":[{"role":"user","content":"warm cache"}]}`)
	proxy.block(true)
	defer proxy.block(false)
	// The parent still reaches the real Redis; only both gateways lose access.
	must(common.RDB.Ping(context.Background()).Err())
	statuses := make(chan int, 2)
	var workers sync.WaitGroup
	for _, port := range []string{"18371", "18372"} {
		workers.Go(func() {
			req, err := http.NewRequest("POST", "http://127.0.0.1:"+port+"/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"cache outage"}]}`))
			must(err)
			req.Header.Set("Authorization", "Bearer sk-"+token.Key)
			req.Header.Set("Content-Type", "application/json")
			response, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
			must(err)
			_, err = io.Copy(io.Discard, response.Body)
			must(err)
			_ = response.Body.Close()
			statuses <- response.StatusCode
		})
	}
	workers.Wait()
	close(statuses)
	successful := 1
	for status := range statuses {
		check(status == 200 || status == 500 || status == 503 || status == 429, fmt.Sprintf("unexpected cache-fault status %d", status))
		if status == 200 {
			successful++
		}
		fmt.Printf("GATEWAY_CACHE_FAULT_RESPONSE status=%d\n", status)
	}
	proxy.block(false)
	request("18372", "/v1/chat/completions", token.Key, `{"model":"gpt-4o","messages":[{"role":"user","content":"cache recovered"}]}`)
	successful++
	eventually("cache recovery settlement", func() bool {
		return model.DB.First(&user, user.Id).Error == nil && user.RequestCount == successful
	})
	must(model.DB.First(&gift, gift.ID).Error)
	must(model.DB.First(&token, token.Id).Error)
	cost := successful * 500
	check(user.Quota == 5600-cost && user.UsedQuota == cost && gift.ConsumedQuota == 600 && gift.RemainingQuota == 0, "Redis outage altered source conservation")
	check(token.UsedQuota == cost && token.RemainQuota == 10000-cost, "Redis outage altered token accounting")
	var pending int64
	must(model.DB.Model(&model.MarketingFunding{}).Where("user_id = ? AND (state <> ? OR reserved_quota <> 0)", user.Id, "settled").Count(&pending).Error)
	check(pending == 0, "Redis fault left unsettled funds")
	fmt.Printf("GATEWAY_CACHE_FAULT_OK two_gateways=true real_socket_disconnect=true shared_redis_untouched=true completed=%d source_conservation=true\n", successful)
}
