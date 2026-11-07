// Package client implements a client for communicating with a Mitsubishi
// Outlander Phev.
package client

import (
	"encoding/hex"
	"fmt"
	log "github.com/sirupsen/logrus"
	"net"
	"sync"
	"time"

	"github.com/buxtronix/phev2mqtt/protocol"
)

const DefaultAddress = "192.168.8.46:8080"

// A Listener is for communicating messages from the vehicle to
// interested clients.
type Listener struct {
	// C has received messages.
	C      chan *protocol.PhevMessage
	mu     sync.Mutex
	stop   bool
	closed bool
}

func (l *Listener) Start() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stop = false
	l.closed = false
	l.C = make(chan *protocol.PhevMessage, 64)
}

func (l *Listener) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.closed {
		l.closed = true
		l.stop = false
		if l.C != nil {
			close(l.C)
		}
	}
}

func (l *Listener) Send(m *protocol.PhevMessage) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed || l.C == nil {
		return
	}
	select {
	case l.C <- m:
	default:
		log.Debug("%PHEV_RECV_LISTENER% message not sent")
	}
}

func (l *Listener) ProcessStop() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stop && !l.closed {
		l.closed = true
		l.stop = false
		if l.C != nil {
			close(l.C)
		}
		return true
	}
	return l.closed
}

type ModelYear int64

const (
	ModelYearUnknown ModelYear = iota
	ModelYear14
	ModelYear18
	ModelYear24
)

func (m ModelYear) String() string {
	switch m {
	case ModelYear14:
		return "MY14"
	case ModelYear18:
		return "MY18"
	case ModelYear24:
		return "MY24"
	default:
		return "Unknown"
	}
}

// A Client is a TCP client to a Phev.
type Client struct {
	// Recv is a channel where incoming messages from the Phev are sent.
	Recv chan *protocol.PhevMessage
	// Send is a channel to send messages to the Phev.
	Send chan *protocol.PhevMessage

	// Settings are settings for the car.
	Settings *protocol.Settings

	listeners []*Listener
	lMu       sync.Mutex

	address string
	conn    net.Conn
	lastRx  time.Time
	started chan struct{}

	key *protocol.SecurityKey

	// Keep track of the model year so we can use the correct registers
	ModelYear ModelYear

	mu        sync.RWMutex
	setRegMu  sync.Mutex
	done      chan struct{}
	closeOnce sync.Once
	closed    bool
}

// An Option configures the client.
type Option func(c *Client)

// AddressOption configures the address to the Phev.
func AddressOption(address string) func(*Client) {
	return func(c *Client) {
		c.address = address
	}
}

// New returns a new client, not yet connected.
func New(opts ...Option) (*Client, error) {
	cl := &Client{
		Recv:      make(chan *protocol.PhevMessage, 64),
		Send:      make(chan *protocol.PhevMessage, 64),
		Settings:  &protocol.Settings{},
		started:   make(chan struct{}, 4),
		done:      make(chan struct{}),
		listeners: []*Listener{},
		address:   DefaultAddress,
		key:       &protocol.SecurityKey{},
		ModelYear: ModelYearUnknown,
	}
	for _, o := range opts {
		o(cl)
	}
	return cl, nil
}

// Create and return a new Listener.
func (c *Client) AddListener() *Listener {
	c.lMu.Lock()
	defer c.lMu.Unlock()
	l := &Listener{}
	l.Start()
	if c.IsClosed() {
		l.Stop()
		return l
	}
	c.listeners = append(c.listeners, l)
	return l
}

func (c *Client) RemoveListener(l *Listener) {
	newL := []*Listener{}
	c.lMu.Lock()
	defer c.lMu.Unlock()
	for _, lis := range c.listeners {
		if lis != l {
			newL = append(newL, lis)
		}
	}
	c.listeners = newL
	l.Stop()
}

// IsClosed reports whether the client connection has been closed.
func (c *Client) IsClosed() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.closed
}

// SendMessage safely enqueues an outgoing message without blocking forever if the connection closes.
func (c *Client) SendMessage(m *protocol.PhevMessage) error {
	select {
	case <-c.done:
		return fmt.Errorf("client connection closed")
	case c.Send <- m:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timed out queueing message to car")
	}
}

// Close closes the client and terminates background goroutines.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.closed = true
		conn := c.conn
		c.mu.Unlock()

		close(c.done)
		if conn != nil {
			err = conn.Close()
		}

		c.lMu.Lock()
		for _, l := range c.listeners {
			l.Stop()
		}
		c.listeners = nil
		c.lMu.Unlock()
	})
	return err
}

// Connect connects to the Phev.
func (c *Client) Connect() error {
	conn, err := net.DialTimeout("tcp", c.address, 10*time.Second)
	if err != nil {
		return err
	}
	log.Info("%PHEV_TCP_CONNECTED%")
	c.mu.Lock()
	c.closed = false
	c.conn = conn
	c.lastRx = time.Now()
	c.mu.Unlock()

	go c.reader()
	go c.writer()
	go c.manage()
	go c.pinger()

	return nil
}

var startTimeout = 35 * time.Second

// Start waits for the client to start.
func (c *Client) Start() error {
	log.Debug("%%PHEV_START_AWAIT%%")
	startTimer := time.NewTimer(startTimeout)
	defer startTimer.Stop()

	select {
	case _, ok := <-c.started:
		if !ok {
			log.Debug("%%PHEV_START_CLOSED%%")
			return fmt.Errorf("receiver closed before getting start request")
		}
		log.Debug("%%PHEV_START_DONE%%")
		return nil
	case <-c.done:
		log.Debug("%%PHEV_START_CLOSED%%")
		return fmt.Errorf("client closed while waiting for start")
	case <-startTimer.C:
		log.Debug("%%PHEV_START_TIMEOUT%%")
		return fmt.Errorf("timed out waiting for start")
	}
}

// SetRegister sets a register on the car, serialized so concurrent writes do not collide on ACKs.
func (c *Client) SetRegister(register byte, value []byte) error {
	if c.IsClosed() {
		return fmt.Errorf("client is closed")
	}
	c.setRegMu.Lock()
	defer c.setRegMu.Unlock()

	if c.IsClosed() {
		return fmt.Errorf("client is closed")
	}

	dataCopy := append([]byte(nil), value...)
	setRegister := func(xor byte, forceXor bool) error {
		return c.SendMessage(&protocol.PhevMessage{
			Type:     protocol.CmdOutSend,
			Ack:      protocol.Request,
			Register: register,
			Data:     dataCopy,
			Xor:      xor,
			ForceXor: forceXor,
		})
	}
	xor := byte(0)
	forceXor := false
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	l := c.AddListener()
	defer c.RemoveListener(l)
SETREG:
	if err := setRegister(xor, forceXor); err != nil {
		return err
	}
	for {
		select {
		case <-c.done:
			return fmt.Errorf("client closed while setting register %02x", register)
		case <-timer.C:
			return fmt.Errorf("timed out attempting to set register %02x", register)
		case msg, ok := <-l.C:
			if !ok {
				return fmt.Errorf("listener channel closed")
			}
			if msg.Type == protocol.CmdInBadEncoding && len(msg.Data) > 0 {
				xor = msg.Data[0]
				forceXor = true
				goto SETREG
			}
			if msg.Type == protocol.CmdInResp && msg.Ack == protocol.Ack && msg.Register == register {
				return nil
			}
		}
	}
}

func (c *Client) nextRecvMsg(deadline time.Time) (*protocol.PhevMessage, error) {
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	for {
		select {
		case <-c.done:
			return nil, fmt.Errorf("client closed")
		case <-timer.C:
			return nil, fmt.Errorf("timed out waiting for message")
		case m, ok := <-c.Recv:
			if !ok {
				return nil, fmt.Errorf("error: receive channel closed")
			}
			return m, nil
		}
	}
}

// Sends periodic pings to the car.
func (c *Client) pinger() {
	pingSeq := byte(0xa)
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case t := <-ticker.C:
			c.mu.RLock()
			closed := c.closed
			lastRx := c.lastRx
			c.mu.RUnlock()
			if closed {
				return
			}
			if t.Sub(lastRx) < 500*time.Millisecond {
				continue
			}
			if err := c.SendMessage(protocol.NewPingRequestMessage(pingSeq)); err != nil {
				return
			}
			pingSeq++
			if pingSeq > 0x63 {
				pingSeq = 0
			}
		}
	}
}

// manages the connection, handling control messages.
func (c *Client) manage() {
	ml := c.AddListener()
	defer c.RemoveListener(ml)
	defer close(c.started)

	notifyStarted := func() {
		select {
		case c.started <- struct{}{}:
		default:
		}
	}

	for m := range ml.C {
		switch m.Type {
		case protocol.CmdInResp:
			if m.Ack == protocol.Request && m.Register == protocol.SettingsRegister {
				_ = c.Settings.FromRegister(m.Data)
			}
		case protocol.CmdInStartResp:
			_ = c.SendMessage(protocol.NewPingRequestMessage(0xa))
		case protocol.CmdInMy24StartReq:
			c.mu.Lock()
			c.ModelYear = ModelYear24
			c.mu.Unlock()
			_ = c.SendMessage(&protocol.PhevMessage{
				Type:     protocol.CmdOutMy24StartResp,
				Register: 0x1,
				Ack:      protocol.Ack,
				Xor:      m.Xor,
				Data:     []byte{0x0},
			})
			log.Debug("%%PHEV_START24_RECV%%")
			notifyStarted()
		case protocol.CmdInMy18StartReq:
			c.mu.Lock()
			c.ModelYear = ModelYear18
			c.mu.Unlock()
			_ = c.SendMessage(&protocol.PhevMessage{
				Type:     protocol.CmdOutMy18StartResp,
				Register: 0x1,
				Ack:      protocol.Ack,
				Xor:      m.Xor,
				Data:     []byte{0x0},
			})
			log.Debug("%%PHEV_START18_RECV%%")
			notifyStarted()
		case protocol.CmdInMy14StartReq:
			c.mu.Lock()
			c.ModelYear = ModelYear14
			c.mu.Unlock()
			_ = c.SendMessage(&protocol.PhevMessage{
				Type:     protocol.CmdOutMy14StartResp,
				Register: 0x1,
				Ack:      protocol.Ack,
				Xor:      m.Xor,
				Data:     []byte{0x0},
			})
			log.Debug("%%PHEV_START14_RECV%%")
			notifyStarted()
		}
	}
	log.Debug("%PHEV_MANAGER_END%%")
}

func (c *Client) reader() {
	defer close(c.Recv)
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		data := make([]byte, 4096)
		n, err := c.conn.Read(data)
		if err != nil {
			if !c.IsClosed() {
				log.Debug("%%PHEV_TCP_READER_ERROR%%: ", err)
			}
			log.Debug("%PHEV_TCP_READER_CLOSE%")
			_ = c.Close()
			return
		}
		c.mu.Lock()
		c.lastRx = time.Now()
		c.mu.Unlock()

		log.Tracef("%%PHEV_TCP_RECV_DATA%%: %s", hex.EncodeToString(data[:n]))
		messages := protocol.NewFromBytes(data[:n], c.key)
		for _, m := range messages {
			log.Debugf("%%PHEV_TCP_RECV_MSG%%: [%02x] %s", m.Xor, m.ShortForm())
			c.lMu.Lock()
			for _, l := range c.listeners {
				l.Send(m)
			}
			c.lMu.Unlock()
			select {
			case <-c.done:
				return
			case c.Recv <- m:
			}
		}
	}
}

func (c *Client) writer() {
	for {
		select {
		case <-c.done:
			log.Debug("%PHEV_TCP_WRITER_CLOSE%")
			return
		case msg, ok := <-c.Send:
			if !ok {
				log.Debug("%PHEV_TCP_WRITER_CLOSE%")
				_ = c.Close()
				return
			}
			if !msg.ForceXor {
				msg.Xor = 0
			}
			data := msg.EncodeToBytes(c.key)
			log.Debugf("%%PHEV_TCP_SEND_MSG%%: [%02x] %s", msg.Xor, msg.ShortForm())
			log.Tracef("%%PHEV_TCP_SEND_DATA%%: %s", hex.EncodeToString(data))
			_ = c.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, err := c.conn.Write(data); err != nil {
				if !c.IsClosed() {
					log.Errorf("%%PHEV_TCP_WRITER_ERROR%%: %v", err)
				}
				log.Debug("%PHEV_TCP_WRITER_CLOSE%")
				_ = c.Close()
				return
			}
		}
	}
}
