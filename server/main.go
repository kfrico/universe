package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

type message struct {
	Type      string  `json:"type"`
	Point     *point  `json:"point,omitempty"`
	Points    []point `json:"points,omitempty"`
	StartTime int64   `json:"startTime,omitempty"`
}

type hub struct {
	mu        sync.Mutex
	points    map[string]point
	conns     map[*websocket.Conn]struct{}
	startTime int64
}

func newHub() *hub {
	return &hub{
		points:    make(map[string]point),
		conns:     make(map[*websocket.Conn]struct{}),
		startTime: time.Now().UnixMilli(),
	}
}

func (h *hub) key(p point) string {
	return fmt.Sprintf("%.6f,%.6f,%.6f", p.X, p.Y, p.Z)
}

func (h *hub) addPoint(p point) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	key := h.key(p)
	if _, exists := h.points[key]; exists {
		return false
	}
	h.points[key] = p
	return true
}

func (h *hub) removePoint(p point) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	key := h.key(p)
	if _, exists := h.points[key]; !exists {
		return false
	}
	delete(h.points, key)
	return true
}

func (h *hub) snapshotPoints() []point {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]point, 0, len(h.points))
	for _, p := range h.points {
		out = append(out, p)
	}
	return out
}

func (h *hub) addConn(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.conns[conn] = struct{}{}
}

func (h *hub) removeConn(conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.conns, conn)
	conn.Close()
}

func (h *hub) broadcast(msg message) {
	payload, err := json.Marshal(msg)
	if err != nil {
		log.Println("broadcast marshal error:", err)
		return
	}

	h.mu.Lock()
	conns := make([]*websocket.Conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()

	for _, c := range conns {
		if err := c.WriteMessage(websocket.TextMessage, payload); err != nil {
			log.Println("write ws error:", err)
			h.removeConn(c)
		}
	}
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func (h *hub) wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade error:", err)
		return
	}
	h.addConn(conn)
	defer h.removeConn(conn)

	initMsg := message{Type: "init", Points: h.snapshotPoints(), StartTime: h.startTime}
	if err := conn.WriteJSON(initMsg); err != nil {
		log.Println("init write error:", err)
		return
	}

	for {
		var msg message
		if err := conn.ReadJSON(&msg); err != nil {
			log.Println("read error:", err)
			return
		}

		switch msg.Type {
		case "add":
			if msg.Point != nil && h.addPoint(*msg.Point) {
				h.broadcast(message{Type: "add", Point: msg.Point})
			}
		case "remove":
			if msg.Point != nil && h.removePoint(*msg.Point) {
				h.broadcast(message{Type: "remove", Point: msg.Point})
			}
		default:
			log.Println("unknown message type:", msg.Type)
		}
	}
}

func main() {
	h := newHub()

	http.HandleFunc("/ws", h.wsHandler)
	http.Handle("/", http.FileServer(http.Dir(".")))

	addr := ":8080"
	log.Println("listening on", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

