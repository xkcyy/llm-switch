// Package cdp 是一个极简的 Chrome DevTools Protocol 客户端。
//
// 只实现本项目需要的三件事：
//   - 发现本机 Electron 应用的调试目标（HTTP /json/list）
//   - 在页面里执行 JS（Runtime.evaluate，支持传参与返回 JSON）
//   - 发送真实鼠标事件（Input.dispatchMouseEvent）——Radix 之类的组件只认
//     pointer 事件，纯 DOM 的 element.click() 打开不了下拉框
//
// 不含任何协议之外的猜测：所有方法都是 CDP 的原生方法名与参数。
package cdp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// ErrClosed 表示连接已关闭。
var ErrClosed = errors.New("CDP 连接已关闭")

// Target 是 /json/list 返回的一个调试目标。
type Target struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

// HTTPBase 返回 CDP 的 HTTP 端点，例如 http://127.0.0.1:9222。
func HTTPBase(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}

// ListTargets 读取调试目标列表。
func ListTargets(ctx context.Context, port int) ([]Target, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, HTTPBase(port)+"/json/list", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("调试端口返回 HTTP %d", resp.StatusCode)
	}
	var list []Target
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, fmt.Errorf("解析调试目标失败: %w", err)
	}
	return list, nil
}

// Alive 判断调试端口是否可用。
func Alive(ctx context.Context, port int) bool {
	targets, err := ListTargets(ctx, port)
	return err == nil && len(targets) > 0
}

type rpcResponse struct {
	ID     int             `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Client 是一个页面级 CDP 连接。
type Client struct {
	conn *websocket.Conn

	mu      sync.Mutex
	nextID  int
	pending map[int]chan rpcResponse
	closed  bool
	readErr error
}

// Dial 连接页面目标的 WebSocket 调试端点。
func Dial(ctx context.Context, wsURL string) (*Client, error) {
	dialer := websocket.Dialer{HandshakeTimeout: 8 * time.Second}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return nil, fmt.Errorf("连接调试端点失败: %w", err)
	}
	c := &Client{conn: conn, pending: map[int]chan rpcResponse{}}
	go c.readLoop()
	return c, nil
}

func (c *Client) readLoop() {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.mu.Lock()
			c.closed = true
			c.readErr = err
			for id, ch := range c.pending {
				close(ch)
				delete(c.pending, id)
			}
			c.mu.Unlock()
			return
		}
		var resp rpcResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			continue
		}
		if resp.ID == 0 {
			continue // 事件通知，本项目用不到
		}
		c.mu.Lock()
		ch, ok := c.pending[resp.ID]
		if ok {
			delete(c.pending, resp.ID)
		}
		c.mu.Unlock()
		if ok {
			ch <- resp
			close(ch)
		}
	}
}

// Close 关闭连接。
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()
	_ = c.conn.Close()
}

// Call 调用一个 CDP 方法并返回原始 result。
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		err := c.readErr
		c.mu.Unlock()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrClosed, err)
		}
		return nil, ErrClosed
	}
	c.nextID++
	id := c.nextID
	ch := make(chan rpcResponse, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	payload := map[string]any{"id": id, "method": method}
	if params != nil {
		payload["params"] = params
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	if err := c.conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		return nil, err
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp, ok := <-ch:
		if !ok {
			return nil, ErrClosed
		}
		if resp.Error != nil {
			return nil, fmt.Errorf("CDP %s 失败: %s", method, resp.Error.Message)
		}
		return resp.Result, nil
	}
}

type evalResult struct {
	Result struct {
		Type        string          `json:"type"`
		Value       json.RawMessage `json:"value"`
		Description string          `json:"description"`
		SubtreeSize int             `json:"subtreeSize"`
	} `json:"result"`
	ExceptionDetails *struct {
		Text      string `json:"text"`
		Exception *struct {
			Description string `json:"description"`
		} `json:"exception"`
	} `json:"exceptionDetails"`
}

// Eval 在页面里执行一段 JS 表达式，并把返回值按 JSON 解出（returnByValue）。
// args 不为 nil 时，expression 必须是一个函数（形如 (args) => …），args 会被序列化后传入。
func (c *Client) Eval(ctx context.Context, expression string, args any, out any) error {
	params := map[string]any{
		"expression":    expression,
		"returnByValue": true,
		"awaitPromise":  true,
		"userGesture":   true,
	}
	if args != nil {
		raw, err := json.Marshal(args)
		if err != nil {
			return err
		}
		params["expression"] = "(" + expression + ")(" + string(raw) + ")"
	}
	raw, err := c.Call(ctx, "Runtime.evaluate", params)
	if err != nil {
		return err
	}
	var res evalResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	if res.ExceptionDetails != nil {
		msg := res.ExceptionDetails.Text
		if res.ExceptionDetails.Exception != nil && res.ExceptionDetails.Exception.Description != "" {
			msg = res.ExceptionDetails.Exception.Description
		}
		return fmt.Errorf("页面脚本异常: %s", strings.TrimSpace(msg))
	}
	if out == nil || len(res.Result.Value) == 0 {
		return nil
	}
	return json.Unmarshal(res.Result.Value, out)
}

// Click 在页面坐标 (x, y) 上模拟一次真实左键点击。
// Radix 等组件依赖 pointerdown/pointerup，纯 JS 的 click() 不会触发。
func (c *Client) Click(ctx context.Context, x, y float64) error {
	base := map[string]any{
		"x": x, "y": y,
		"button": "left", "buttons": 1, "clickCount": 1,
		"pointerType": "mouse",
	}
	for _, t := range []string{"mouseMoved", "mousePressed", "mouseReleased"} {
		params := map[string]any{"type": t}
		for k, v := range base {
			params[k] = v
		}
		if t == "mouseReleased" {
			params["buttons"] = 0
		}
		if _, err := c.Call(ctx, "Input.dispatchMouseEvent", params); err != nil {
			return err
		}
		time.Sleep(30 * time.Millisecond)
	}
	return nil
}

// Key 发送一次按键（用于关闭 Radix 弹窗等）。
func (c *Client) Key(ctx context.Context, key string) error {
	type keyInfo struct {
		code string
		vk   int
	}
	table := map[string]keyInfo{
		"Escape": {"Escape", 27},
		"Enter":  {"Enter", 13},
	}
	info, ok := table[key]
	if !ok {
		return fmt.Errorf("不支持的按键：%s", key)
	}
	for _, t := range []string{"keyDown", "keyUp"} {
		params := map[string]any{
			"type":                  t,
			"key":                   key,
			"code":                  info.code,
			"windowsVirtualKeyCode": info.vk,
			"nativeVirtualKeyCode":  info.vk,
		}
		if _, err := c.Call(ctx, "Input.dispatchKeyEvent", params); err != nil {
			return err
		}
	}
	return nil
}
