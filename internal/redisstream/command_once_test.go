package redisstream

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Minimal RESP fault peer, not a broker. Drop the XADD response after reading
// the entire command, and count actual network attempts across connections.
func TestCurrentAccountBaselineCommandNoNetworkRetry(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var wg sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(5 * time.Second))
				r := bufio.NewReader(c)
				for {
					args, e := readTestRESPCommand(r)
					if e != nil {
						return
					}
					switch strings.ToLower(args[0]) {
					case "hello":
						_, _ = io.WriteString(c, "-ERR unknown command 'hello'\r\n")
					case "xadd":
						calls.Add(1)
						return
					default:
						_, _ = io.WriteString(c, "+OK\r\n")
					}
				}
			}()
		}
	}()
	client := redis.NewClient(&redis.Options{Addr: l.Addr().String(), Protocol: 2, MaxRetries: 5, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, DisableIdentity: true})
	p := NewRedisCommandPublisher(client)
	t.Cleanup(func() { _ = p.Close(); _ = l.Close(); <-done; wg.Wait() })
	if p.onceClient.Options().MaxRetries != 0 || client.Options().MaxRetries != 5 {
		t.Fatal("retry settings leaked to legacy client")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err = p.PublishCommandBodyOnce(ctx, "relay:test:v1:huaxin:synthetic:cmd.trade", []byte(`{"synthetic_only":true}`)); err == nil {
		t.Fatal("lost response reported success")
	}
	if calls.Load() != 1 {
		t.Fatal("XADD retried", calls.Load())
	}
}

func readTestRESPCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(line, "*") {
		return nil, fmt.Errorf("not an array")
	}
	n, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || n < 1 || n > 32 {
		return nil, fmt.Errorf("bad count")
	}
	args := make([]string, n)
	for i := range args {
		line, err = r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if !strings.HasPrefix(line, "$") {
			return nil, fmt.Errorf("not a bulk string")
		}
		size, e := strconv.Atoi(strings.TrimSpace(line[1:]))
		if e != nil || size < 0 || size > 1<<20 {
			return nil, fmt.Errorf("bad size")
		}
		b := make([]byte, size+2)
		if _, err = io.ReadFull(r, b); err != nil {
			return nil, err
		}
		args[i] = string(b[:size])
	}
	return args, nil
}

func TestCurrentAccountBaselineRegisteredBytesLocalRedis(t *testing.T) {
	client := localReceiptRedis(t)
	p := NewRedisCommandPublisher(client)
	defer p.Close()
	const key = "relay:test:v1:huaxin:synthetic:cmd.trade"
	body := []byte(" \n{\"synthetic_only\":true, \"price\":10.125000}\n")
	result, err := p.PublishCommandBodyOnce(context.Background(), key, body)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := client.XRange(context.Background(), key, "-", "+").Result()
	if err != nil || len(rows) != 1 || rows[0].ID != result.StreamID || rows[0].Values["body"] != string(body) || result.BodyBytes != len(body) {
		t.Fatal(rows, result, err)
	}
}
