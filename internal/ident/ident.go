package ident

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
)

// A very short identity query so a scanner that only knows IP:port can
// learn the libp2p peer ID, then complete the real handshake with go-libp2p.
// This is not a mesh protocol: transport, encryption, and streams stay on
// libp2p TCP at the same port number.
const (
	request  = "PTC/1 IDENT\n"
	respPref = "PTC/1 "
)

// ServeUDP answers identity queries on the same host:port as the TCP
// libp2p listener, using UDP.
func ServeUDP(ctx context.Context, hostport, peerID string, log *slog.Logger) error {
	pc, err := net.ListenPacket("udp", hostport)
	if err != nil {
		return fmt.Errorf("ident udp listen %s: %w", hostport, err)
	}
	if log == nil {
		log = slog.Default()
	}
	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Debug("ident udp read", "err", err)
				continue
			}
			if !bytes.HasPrefix(buf[:n], []byte("PTC/1 IDENT")) {
				continue
			}
			_, err = pc.WriteTo([]byte(respPref+peerID+"\n"), addr)
			if err != nil {
				log.Debug("ident udp write", "err", err, "to", addr)
			}
		}
	}()
	return nil
}

// Query asks hostport (UDP) for the remote peer ID.
func Query(ctx context.Context, hostport string) (string, error) {
	var d net.Dialer
	if deadline, ok := ctx.Deadline(); ok {
		d.Deadline = deadline
	} else {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 400*time.Millisecond)
		defer cancel()
		if deadline, ok := ctx.Deadline(); ok {
			d.Deadline = deadline
		}
	}
	c, err := d.DialContext(ctx, "udp", hostport)
	if err != nil {
		return "", err
	}
	defer c.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(deadline)
	} else {
		_ = c.SetDeadline(time.Now().Add(400 * time.Millisecond))
	}
	if _, err := c.Write([]byte(request)); err != nil {
		return "", err
	}
	buf := make([]byte, 256)
	n, err := c.Read(buf)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(string(buf[:n]))
	if !strings.HasPrefix(line, respPref) {
		return "", fmt.Errorf("unexpected ident response from %s", hostport)
	}
	id := strings.TrimSpace(strings.TrimPrefix(line, respPref))
	if id == "" {
		return "", fmt.Errorf("empty peer id from %s", hostport)
	}
	return id, nil
}
