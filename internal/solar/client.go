package solar

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/goburrow/modbus"
)

type Client struct {
	ip      string
	port    uint16
	slaveID byte
	handler *modbus.TCPClientHandler
	client  modbus.Client
}

func NewClient(ip string, port uint16, slaveID byte) (*Client, error) {
	h := modbus.NewTCPClientHandler(fmt.Sprintf("%s:%d", ip, port))
	h.Timeout = 60 * time.Second
	h.SlaveId = slaveID
	if err := h.Connect(); err != nil {
		return nil, fmt.Errorf("modbus connect: %w", err)
	}
	c := modbus.NewClient(h)
	return &Client{ip: ip, port: port, slaveID: slaveID, handler: h, client: c}, nil
}

func (c *Client) Close() error {
	if c.handler != nil {
		return c.handler.Close()
	}
	return nil
}

func (c *Client) doPDU(pdu modbus.ProtocolDataUnit) (*modbus.ProtocolDataUnit, error) {
	adu, err := c.handler.Encode(&pdu)
	if err != nil {
		return nil, fmt.Errorf("err encoding pdu: %v", err)
	}

	aduResp, err := c.handler.Send(adu)
	if err != nil {
		return nil, fmt.Errorf("err sending pdu and egtting resp: %v", err)
	}

	pduResp, err := c.handler.Decode(aduResp)
	if err != nil {
		return nil, fmt.Errorf("err decoding pdu from adu resp: %v", err)
	}

	return pduResp, nil
}

func (c *Client) QueryDeviceInfos() { // yes that's what it's called
	resp, err := c.doPDU(modbus.ProtocolDataUnit{
		FunctionCode: 0x2B,
		Data: []byte{
			0x0e,
			0x03,
			0x87,
		},
	})

	if err != nil {
		log.Printf("failed to query device infos %v\n", err)
		return
	}

	log.Printf("resp: %v\ntotal len %d\n", resp.Data, len(resp.Data))
	if len(resp.Data) < 5 {
		log.Printf("expected at least 5 bytes in response, found: %d\n", len(resp.Data))
		return
	}

	deviceIdCode := resp.Data[1]
	consistencyLevel := resp.Data[2]
	more := resp.Data[3]
	nextObjId := resp.Data[4]
	numObjects := resp.Data[5]

	log.Printf("device_id_code=%d, consistency_level=%d, more=%d, next_obj_id=%d, num_objects=%d\n", deviceIdCode, consistencyLevel, more, nextObjId, numObjects)

	type Obj struct {
		id      uint16
		data    []byte
		dataMap map[string]string
		/*
		   from interface defs file:

		   1. Device Model
		   2. Device software version
		   3. Interface protocol version
		   4. ESN
		   5. Device ID (assigned by NEs; 0 indicates the master device into which the modbus card is isnserted)
		   6. Feature Version
		   7. (unlisted)
		   8. Device Type
		*/
	}

	objs := []Obj{}
	cursor := resp.Data[6:]

	for cursor != nil {
		if len(cursor) < 2 {
			break
		}
		objId, objLen := cursor[0], cursor[1]
		if len(cursor)-2 < int(objLen) {
			log.Printf("invalid len. object reported length of %d but only %d bytes left", objLen, len(cursor)-2)
			break
		}
		obj := Obj{
			id:      uint16(objId),
			data:    cursor[2 : 2+(objLen)],
			dataMap: make(map[string]string),
		}

		dataStr := string(obj.data)
		if strings.Contains(dataStr, "=") {
			props := strings.Split(dataStr, ";")
			for _, prop := range props {
				k, v, _ := strings.Cut(prop, "=")
				obj.dataMap[k] = v
			}
		}

		objs = append(objs, obj)

		if len(cursor) > int(objLen)+2 {
			cursor = cursor[2+objLen:]
		} else {
			cursor = nil
		}
	}

	log.Printf("retrieved %d objs", len(objs))

	for _, obj := range objs {
		log.Printf("Obj, id=%d, data=%v\nprops=%v\n\n", obj.id, obj.data, obj.dataMap)
	}
}
