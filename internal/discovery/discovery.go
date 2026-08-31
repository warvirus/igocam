// WS-Discovery 응답기 - ONVIF 디바이스 탐지 (멀티캐스트 239.255.255.250:3702)
package discovery

import (
	"fmt"
	"net"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"igocam/internal/onvif"
)

const (
	multicastAddr = "239.255.255.250"
	multicastPort = 3702
	probeAction   = "http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe"
)

// Server WS-Discovery 응답기.
// 여러 카메라가 공유하는 단일 응답기로 동작한다 (한 프로세스만 :3702 바인딩 가능).
type Server struct {
	services []*onvif.Service
	port     int
	done     chan struct{}
	doneOnce sync.Once
	mu       sync.Mutex
	sock     *net.UDPConn
}

// New WS-Discovery 서버 생성.
func New(services []*onvif.Service, port int) *Server {
	if port <= 0 {
		port = multicastPort
	}
	return &Server{
		services: services,
		port:     port,
		done:     make(chan struct{}),
	}
}

// Start 서버를 시작한다.
func (s *Server) Start() error {
	s.mu.Lock()
	if s.sock != nil {
		s.mu.Unlock()
		return nil
	}

	addr := &net.UDPAddr{IP: net.IPv4zero, Port: s.port}
	sock, err := net.ListenUDP("udp4", addr)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	sock.SetReadBuffer(4096)
	s.sock = sock
	s.mu.Unlock() // announce는 락 없이 호출

	s.joinMulticast(sock)
	go s.run()
	s.announce("Hello")
	return nil
}

// Stop 서버를 정지한다.
func (s *Server) Stop() {
	s.doneOnce.Do(func() {
		close(s.done)
	})
	s.announce("Bye")
	s.mu.Lock()
	sock := s.sock
	s.sock = nil
	s.mu.Unlock()
	if sock != nil {
		sock.Close()
	}
}

// joinMulticast 멀티캐스트 그룹에 best-effort로 가입한다.
func (s *Server) joinMulticast(sock *net.UDPConn) {
	group := net.ParseIP(multicastAddr)
	ifaces, err := net.Interfaces()
	if err != nil {
		return
	}
	joined := false
	// INADDR_ANY (와일드카드) 가입 먼저.
	{
		mreq := &syscall.IPMreq{
			Multiaddr: [4]byte{group.To4()[0], group.To4()[1], group.To4()[2], group.To4()[3]},
		}
		raw, err := sock.SyscallConn()
		if err == nil {
			_ = raw.Control(func(fd uintptr) {
				if err := syscall.SetsockoptIPMreq(int(fd), syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, mreq); err == nil {
					joined = true
				}
			})
		}
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil || ip.To4() == nil {
				continue
			}
			mreq := &syscall.IPMreq{
				Multiaddr: [4]byte{group.To4()[0], group.To4()[1], group.To4()[2], group.To4()[3]},
				Interface: [4]byte{ip.To4()[0], ip.To4()[1], ip.To4()[2], ip.To4()[3]},
			}
			raw, err := sock.SyscallConn()
			if err != nil {
				continue
			}
			_ = raw.Control(func(fd uintptr) {
				_ = syscall.SetsockoptIPMreq(int(fd), syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, mreq)
			})
			joined = true
		}
	}
	_ = joined
}

// run 수신 루프.
func (s *Server) run() {
	buf := make([]byte, 4096)
	for {
		s.mu.Lock()
		sock := s.sock
		s.mu.Unlock()
		if sock == nil {
			return
		}
		sock.SetReadDeadline(time.Now().Add(time.Second))
		n, addr, err := sock.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-s.done:
				return
			default:
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				continue
			}
		}
		msg := string(buf[:n])
		if s.isProbeRequest(msg) {
			s.handleProbe(msg, addr)
		}
	}
}

// isProbeRequest 진짜 Probe 요청인지 확인 (ProbeMatch 응답 제외).
func (s *Server) isProbeRequest(msg string) bool {
	if strings.Contains(msg, "ProbeMatch") {
		return false
	}
	if strings.Contains(msg, probeAction) {
		return true
	}
	return probeRE.MatchString(msg)
}

var probeRE = regexp.MustCompile(`<(?:[\w.-]+:)?Probe[\s/>]`)

// handleProbe Probe에 각 카메라별 ProbeMatch로 응답.
func (s *Server) handleProbe(msg string, addr *net.UDPAddr) {
	s.mu.Lock()
	sock := s.sock
	s.mu.Unlock()
	if sock == nil {
		return
	}
	relatesTo := "urn:uuid:" + fmt.Sprint(time.Now().UnixNano())
	if m := messageIDRE.FindStringSubmatch(msg); len(m) > 1 {
		relatesTo = m[1]
	}
	for _, svc := range s.services {
		response := svc.CreateProbeMatch(relatesTo)
		_, _ = sock.WriteToUDP([]byte(response), addr)
	}
}

var messageIDRE = regexp.MustCompile(`MessageID>(.+?)<`)

// announce Hello/Bye를 멀티캐스트한다. best-effort, 실패 무시.
func (s *Server) announce(action string) {
	s.mu.Lock()
	sock := s.sock
	s.mu.Unlock()
	if sock == nil {
		return
	}
	for _, svc := range s.services {
		msg := buildAnnouncement(action, svc)
		_, _ = sock.WriteToUDP([]byte(msg), &net.UDPAddr{
			IP: net.ParseIP(multicastAddr), Port: s.port,
		})
	}
}

// buildAnnouncement Hello/Bye SOAP 메시지 생성.
func buildAnnouncement(action string, svc *onvif.Service) string {
	messageID := fmt.Sprintf("urn:uuid:%d", time.Now().UnixNano())
	// 사용자 제어 값(camera_name, onvif_url)은 XML 이스케이프.
	xmlEscape := func(s string) string {
		r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
		return r.Replace(s)
	}
	return fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?>`+
			`<soap:Envelope xmlns:soap="http://www.w3.org/2003/05/soap-envelope" `+
			`xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing" `+
			`xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" `+
			`xmlns:dn="http://www.onvif.org/ver10/network/wsdl" `+
			`xmlns:tds="http://www.onvif.org/ver10/device/wsdl">`+
			`<soap:Header><wsa:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</wsa:To>`+
			`<wsa:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/%s</wsa:Action>`+
			`<wsa:MessageID>%s</wsa:MessageID></soap:Header>`+
			`<soap:Body><d:%s>`+
			`<wsa:EndpointReference><wsa:Address>%s</wsa:Address></wsa:EndpointReference>`+
			`<d:Types>dn:NetworkVideoTransmitter tds:Device</d:Types>`+
			`<d:Scopes>onvif://www.onvif.org/type/video_encoder onvif://www.onvif.org/Profile/Streaming onvif://www.onvif.org/name/%s</d:Scopes>`+
			`<d:XAddrs>%s</d:XAddrs>`+
			`<d:MetadataVersion>1</d:MetadataVersion>`+
			`</d:%s></soap:Body></soap:Envelope>`,
		action, messageID, action,
		xmlEscape(svc.DeviceUUID()), xmlEscape(svc.CameraName()), xmlEscape(svc.OnvifURL()),
		action,
	)
}

// SendProbe 하나의 Probe를 보내고 ProbeMatch 응답을 반환한다 (셀프테스트용).
func SendProbe(host string, port int, timeout time.Duration) []string {
	probe := fmt.Sprintf(
		`<?xml version="1.0" encoding="UTF-8"?>`+
			`<soap:Envelope xmlns:soap="http://www.w3.org/2003/05/soap-envelope" `+
			`xmlns:tds="http://www.onvif.org/ver10/device/wsdl" `+
			`xmlns:tns="http://schemas.xmlsoap.org/ws/2005/04/discovery" `+
			`xmlns:wsa="http://schemas.xmlsoap.org/ws/2004/08/addressing">`+
			`<soap:Header>`+
			`<wsa:Action>%s</wsa:Action>`+
			`<wsa:MessageID>urn:uuid:%d</wsa:MessageID>`+
			`<wsa:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</wsa:To>`+
			`</soap:Header>`+
			`<soap:Body><tns:Probe><tns:Types>tds:Device</tns:Types></tns:Probe></soap:Body>`+
			`</soap:Envelope>`,
		probeAction, time.Now().UnixNano(),
	)
	addr, err := net.ResolveUDPAddr("udp4", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return nil
	}
	conn, err := net.DialUDP("udp4", nil, addr)
	if err != nil {
		return nil
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout))
	if _, err := conn.Write([]byte(probe)); err != nil {
		return nil
	}
	var replies []string
	buf := make([]byte, 8192)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			break
		}
		text := string(buf[:n])
		if strings.Contains(text, "ProbeMatch") {
			replies = append(replies, text)
		}
	}
	return replies
}
