package emulator

import (
	"encoding/hex"
	log "github.com/sirupsen/logrus"
	"net"
	"sync"
	"time"

	"github.com/buxtronix/phev2mqtt/client"
	"github.com/buxtronix/phev2mqtt/protocol"
)

type connState int

const (
	conClosed connState = iota
	conTCPOpen
	conSecInit
	conRegisterStart
	conEstablished
)

func NewConnection(conn net.Conn, car *Car) *Connection {
	log.Infof("Connection received!\n")
	return &Connection{
		state:          conClosed,
		car:            car,
		conn:           conn,
		key:            &protocol.SecurityKey{},
		Send:           make(chan *protocol.PhevMessage, 64),
		done:           make(chan struct{}),
		listeners:      []*client.Listener{},
		stagedSettings: make(map[byte]byte),
		startReqCmd:    protocol.CmdInMy18StartReq,
	}
}

// A Connection implements a client connection service.
type Connection struct {
	car  *Car
	conn net.Conn
	key  *protocol.SecurityKey
	Send chan *protocol.PhevMessage

	done      chan struct{}
	closeOnce sync.Once

	listeners []*client.Listener
	lMu       sync.Mutex

	state connState

	registerIndex  int
	settingsSender *protocol.SettingsSender
	stagedSettings map[byte]byte
	startReqCmd    byte
}

// SendMsg enqueues a message for transmission unless the connection has closed.
func (s *Connection) SendMsg(msg *protocol.PhevMessage) bool {
	if msg == nil {
		return false
	}
	select {
	case <-s.done:
		return false
	case s.Send <- msg:
		return true
	}
}

func (s *Connection) Close() error {
	var err error
	s.closeOnce.Do(func() {
		s.state = conClosed
		close(s.done)

		s.lMu.Lock()
		for _, lis := range s.listeners {
			lis.Stop()
		}
		s.listeners = nil
		s.lMu.Unlock()

		if s.car != nil {
			s.car.removeConnection(s)
		}
		if s.conn != nil {
			err = s.conn.Close()
		}
	})
	return err
}

// Create and return a new Listener.
func (s *Connection) AddListener() *client.Listener {
	s.lMu.Lock()
	defer s.lMu.Unlock()
	l := &client.Listener{}
	l.Start()
	s.listeners = append(s.listeners, l)
	return l
}

func (s *Connection) RemoveListener(l *client.Listener) {
	newL := []*client.Listener{}
	s.lMu.Lock()
	defer s.lMu.Unlock()
	for _, lis := range s.listeners {
		if lis != l {
			newL = append(newL, lis)
		}
	}
	s.listeners = newL
}

func (s *Connection) Start() {
	s.state = conTCPOpen
	go s.reader()
	go s.writer()
	go s.manage()
}

func (s *Connection) reader() {
	defer s.Close()
	for {
		_ = s.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		data := make([]byte, 4096)
		n, err := s.conn.Read(data)
		if err != nil {
			log.Debugf("%%PHEV_SVC_READER_ERROR%% %v", err)
			return
		}
		log.Tracef("%%PHEV_SVC_RECV_RAW%%: %s", hex.EncodeToString(data[:n]))
		msgs := protocol.NewFromBytes(data[:n], s.key)
		for _, m := range msgs {
			if m.Type != protocol.CmdOutPingReq {
				log.Debugf("%%PHEV_SVC_RCV_MSG%%: %s", m.ShortForm())
			}
			s.lMu.Lock()
			for _, l := range s.listeners {
				l.Send(m)
			}
			s.lMu.Unlock()
		}
	}
}

func (s *Connection) writer() {
	for {
		select {
		case <-s.done:
			return
		case msg, ok := <-s.Send:
			if !ok {
				log.Debug("%PHEV_SVC_SEND_CLOSE%")
				_ = s.Close()
				return
			}
			if msg.Type != protocol.CmdInPingResp {
				log.Debugf("%%PHEV_SVC_SND_MSG%%: %s", msg.ShortForm())
			}
			data := msg.EncodeToBytes(s.key)
			_ = s.conn.SetWriteDeadline(time.Now().Add(15 * time.Second))
			if _, err := s.conn.Write(data); err != nil {
				log.Debugf("%%PHEV_SVC_WRITE_ERR%%: %v", err)
				_ = s.Close()
				return
			}
		}
	}
}
