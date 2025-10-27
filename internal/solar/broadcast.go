package solar

import (
	"fmt"
	"log"
	"net"
	"time"
)

func (c *Client) BroadcastHello(dst net.IP, self net.IP) error {
	return BroadcastHello(dst, self)
}

// broadly reverse engineered from hisolar's broadcast hello packet
// just says "hello inverter, i'm on this IP", then the inverter allows us to connect
func BroadcastHello(dst net.IP, self net.IP) error {
	laddr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	raddr := &net.UDPAddr{IP: dst, Port: 6600}

	self4 := self.To4()
	if self4 == nil {
		return fmt.Errorf("couldn't convert self IP %v to ipv4", self)
	}

	packet := []byte{
		'Z', 'Z', 'Z', 'Z',
		0, 65, 58, 4,
		self4[0],
		self4[1],
		self4[2],
		self4[3],
	}

	conn, err := net.DialUDP("udp", laddr, raddr)
	if err != nil {
		return fmt.Errorf("failed to dial broadcast addr %v: %v", dst, err)
	}
	defer conn.Close()

	n, err := conn.Write(packet)
	if err != nil {
		return fmt.Errorf("failed to send broadcast hello (%v): %v", packet, err)
	}

	log.Printf("Sent broadcast discovery packet (%d bytes)\n", n)

	resp := make([]byte, 8192)
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err = conn.Read(resp)
	if err != nil {
		log.Printf("Failed to read a response to broadcast hello, but continuing anyway: %v", err)
		return nil
	}
	log.Printf("received broadcast hello response (%d bytes): %v", n, resp)
	return nil
}
