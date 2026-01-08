package solar

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/lachlan2k/huawei-solar-mqtt-relay/internal/modbus"
)

type Client struct {
	conn *modbus.ModbusConn
}

func NewClient(conn *modbus.ModbusConn) *Client {
	return &Client{conn: conn}
}

func (c *Client) Run(ctx context.Context) error {
	return c.conn.Run(ctx)
}
func (c *Client) Close() error {
	return c.conn.Close()
}

func (c *Client) QueryDeviceInfos(ctx context.Context) { // yes that's what it's called
	resp, err := c.conn.FunctionCall(ctx, 0x2B, []byte{
		0x0e,
		0x03,
		0x87,
	})

	if err != nil {
		slog.Error("failed to query device infos", "err", err)
		return
	}

	slog.Debug("query device infos response", "response", resp)
	if len(resp.Data) < 5 {
		slog.Warn("expected at least 5 bytes in response, found", "len", len(resp.Data))
		return
	}

	deviceIdCode := resp.Data[1]
	consistencyLevel := resp.Data[2]
	more := resp.Data[3]
	nextObjId := resp.Data[4]
	numObjects := resp.Data[5]

	slog.Debug("query device infos response", "device_id_code", deviceIdCode, "consistency_level", consistencyLevel, "more", more, "next_obj_id", nextObjId, "num_objects", numObjects)

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
			slog.Warn("invalid len. object reported length of", "obj_len", objLen, "bytes_left", len(cursor)-2)
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

	slog.Debug("retrieved device infos", "num_objects", len(objs))

	for _, obj := range objs {
		slog.Info("device info", "id", obj.id, "data", fmt.Sprintf("%v", obj.data), "props", obj.dataMap)
	}
}
