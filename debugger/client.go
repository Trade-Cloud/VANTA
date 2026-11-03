package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/Trade-Cloud/vanta/internal/debugproto"
)

var errNotConnected = errors.New("нет подключения")

// Client держит TCP-соединение с VANTA и сам переподключается.
// Колбэки вызываются из фоновой горутины — UI трогать только через fyne.Do.
type Client struct {
	OnMessage func(debugproto.Message)
	OnState   func(connected bool, err error)

	mu   sync.Mutex
	addr string
	conn net.Conn
}

func NewClient(addr string) *Client {
	return &Client{addr: addr}
}

func (c *Client) Addr() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.addr
}

// SetAddr меняет адрес и рвёт текущее соединение — Run переподключится сам.
func (c *Client) SetAddr(addr string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.addr = addr
	if c.conn != nil {
		_ = c.conn.Close()
	}
}

// Run — бесконечный цикл подключения и чтения. Запускать в отдельной горутине.
func (c *Client) Run() {
	for {
		conn, err := net.DialTimeout("tcp", c.Addr(), time.Second)
		if err != nil {
			c.OnState(false, err)
			time.Sleep(time.Second)
			continue
		}
		c.mu.Lock()
		c.conn = conn
		c.mu.Unlock()
		c.OnState(true, nil)

		sc := bufio.NewScanner(conn)
		sc.Buffer(make([]byte, 64*1024), 8<<20) // дерево UI может быть большим
		for sc.Scan() {
			var m debugproto.Message
			if json.Unmarshal(sc.Bytes(), &m) == nil {
				c.OnMessage(m)
			}
		}

		c.mu.Lock()
		c.conn = nil
		c.mu.Unlock()
		_ = conn.Close()
		c.OnState(false, sc.Err())
		time.Sleep(500 * time.Millisecond)
	}
}

func (c *Client) Send(cmd debugproto.Command) error {
	data, err := json.Marshal(debugproto.Message{
		Type:    debugproto.TypeCommand,
		Time:    time.Now(),
		Command: &cmd,
	})
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return errNotConnected
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	_, err = c.conn.Write(append(data, '\n'))
	return err
}
