package modbus

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/exp/constraints"
	"golang.org/x/sync/errgroup"
)

type ModbusConn struct {
	conn    net.Conn
	txId    *atomic.Uint32
	slaveId uint8

	aduRxCh chan *ModbusTCPADU
	aduTxCh chan *ModbusTCPADU

	waitersMu sync.Mutex
	waiters   map[uint16]chan *ModbusTCPADU
}

func NewModbusConn(conn net.Conn) *ModbusConn {
	txId := atomic.Uint32{} // atomic doesn't give us u16. u32 will overflow during conversion and thats fine
	txId.Store(1234)

	return &ModbusConn{
		conn: conn,
		txId: &txId,
	}
}

func (c *ModbusConn) Run(parentCtx context.Context) error {
	g, ctx := errgroup.WithContext(parentCtx)

	g.Go(func() error {
		return c.receiver(ctx)
	})

	g.Go(func() error {
		return c.transmitter(ctx)
	})

	g.Go(func() error {
		return c.fanout(ctx)
	})

	return g.Wait()
}

func (c *ModbusConn) receiver(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		packet := &ModbusTCPADU{}
		err := packet.Scan(c.conn)
		if err != nil {
			return err
		}
		c.aduRxCh <- packet
	}
}

func (c *ModbusConn) transmitter(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:

		}

		packet := <-c.aduTxCh
		b := packet.Marshal()

		_, err := c.conn.Write(b)
		if err != nil {
			return err
		}
	}
}

func (c *ModbusConn) fanout(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		packet := <-c.aduRxCh

		c.waitersMu.Lock()

		// Find who's waiting for it
		ch, ok := c.waiters[packet.TransactionID]
		delete(c.waiters, packet.TransactionID)

		c.waitersMu.Unlock()

		if !ok {
			continue
		}

		ch <- packet
	}
}

func (c *ModbusConn) waiter(transactionID uint16) chan *ModbusTCPADU {
	c.waitersMu.Lock()
	defer c.waitersMu.Unlock()

	c.waiters[transactionID] = make(chan *ModbusTCPADU, 1)
	return c.waiters[transactionID]
}

func (c *ModbusConn) FunctionCall(ctx context.Context, fc uint8, data []byte) (*ModbusTCPADU, error) {
	transactionID := uint16(c.txId.Add(1))
	req := &ModbusTCPADU{
		ModbusMBAPHeader: ModbusMBAPHeader{
			TransactionID: uint16(transactionID),
			ProtocolID:    0x0000,
			Length:        uint16(len(data) + 2), // unit id + fc
			UnitID:        c.slaveId,
		},
		FunctionCode: fc,
		Data:         data,
	}

	resultCh := c.waiter(transactionID)
	c.aduTxCh <- req

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		return result, nil
	}
}

func (c *ModbusConn) ReadHoldingRegistersU16(ctx context.Context, address, quantity uint16) ([]byte, error) {
	if quantity < 1 || quantity > 125 {
		return nil, fmt.Errorf("modbus: quantity '%v' must be between '%v' and '%v',", quantity, 1, 125)
	}

	resp, err := c.FunctionCall(ctx, 0x03, []byte{
		0x00, 0x06, 0x00, 0x00,
		byte(address >> 8), byte(address & 0xFF),
		byte(quantity >> 8), byte(quantity & 0xFF),
	})
	if err != nil {
		return nil, fmt.Errorf("modbus: failed to make call to read holding registers: %v", err)
	}

	if len(resp.Data) < 2 {
		return nil, fmt.Errorf("modbus: response data size '%d' is less than expected '%d'", len(resp.Data), 2)
	}

	count := binary.BigEndian.Uint16(resp.Data[0:2])
	if count != quantity*2 {
		return nil, fmt.Errorf("modbus: response data size '%d' does not match requested '%d' registers", count, quantity*2)
	}

	if count != uint16(len(resp.Data)-2) {
		return nil, fmt.Errorf("modbus: response data payload size '%d' does not match expected '%d'", count, len(resp.Data)-2)
	}

	return resp.Data[2:], nil
}

func ReadHoldingRegisters[T constraints.Integer | constraints.Float](c *ModbusConn, ctx context.Context, address, quantity uint16) ([]T, error) {
	size := intDataSize(T(0))
	total := int(quantity) * size / 2
	if total > 125 {
		return nil, fmt.Errorf("modbus: reading %d values results in %d u16 registesr, which is more than 125", quantity, total)
	}

	buff, err := c.ReadHoldingRegistersU16(ctx, address, uint16(total))
	if err != nil {
		return nil, err
	}

	results := make([]T, quantity)
	count := binary.BigEndian.Uint16(buff[:2])
	if count != uint16(total) {
		return nil, fmt.Errorf("modbus: read %d u16 registers, but only got %d", total, count)
	}
	if count != uint16(len(buff)-2) {
		return nil, fmt.Errorf("modbus: read %d u16 registers, but payload is only %d bytes", total, len(buff)-2)
	}

	r := bytes.NewReader(buff[2:])

	for i := range results {
		binary.Read(r, binary.BigEndian, &results[i])
	}
	return results, nil
}

func ReadHoldingRegister[T constraints.Integer | constraints.Float](c *ModbusConn, ctx context.Context, address uint16) (T, error) {
	res, err := ReadHoldingRegisters[T](c, ctx, address, 1)
	if err != nil {
		return T(0), err
	}
	return res[0], nil
}

func ReadHoldingRegisterString(c *ModbusConn, ctx context.Context, address uint16, size uint16) (string, error) {
	// +! null terminator
	res, err := ReadHoldingRegisters[byte](c, ctx, address, size+1)
	if err != nil {
		return "", err
	}

	return strings.TrimRight(string(res[:size]), "\x00"), nil
}

// from encoding/binary, but removed slice types, and made it generic
func intDataSize[T constraints.Integer | constraints.Float](data T) int {
	switch any(data).(type) {
	case bool, int8, uint8, *bool, *int8, *uint8:
		return 1
	case int16, uint16, *int16, *uint16:
		return 2
	case int32, uint32, *int32, *uint32:
		return 4
	case int64, uint64, *int64, *uint64:
		return 8
	case float32, *float32:
		return 4
	case float64, *float64:
		return 8
	}
	return 0
}

// Reads exactly 1 MBAP header and PDU from the client, writes it to the server
// Then reads exactly 1 MBAP header and PDU from the server, writes it to the client
func ProxyModbusCallUnbuffered(client io.ReadWriter, server io.ReadWriter) error {
	header := ModbusMBAPHeader{}
	err := header.Scan(client)
	if err != nil {
		return err
	}

	_, err = server.Write(header.Marshal())
	if err != nil {
		return fmt.Errorf("failed to write modbus header: %v", err)
	}

	// We've already read the unit code, so -1, but we've not read the function code yet (so no need to do -2)
	_, err = io.CopyN(server, client, int64(header.Length)-1)
	if err != nil {
		return fmt.Errorf("failed to copy modbus packet data: %v", err)
	}

	responseHeader := ModbusMBAPHeader{}
	err = responseHeader.Scan(server)
	if err != nil {
		return fmt.Errorf("failed to read response header: %v", err)
	}

	if header.TransactionID != responseHeader.TransactionID {
		slog.Warn("unexpected transaction id", "expected", header.TransactionID, "actual", responseHeader.TransactionID)
	}

	_, err = client.Write(responseHeader.Marshal())
	if err != nil {
		return fmt.Errorf("failed to write modbus header: %v", err)
	}

	_, err = io.CopyN(client, server, int64(responseHeader.Length)-1)
	if err != nil {
		return fmt.Errorf("failed to copy data: %v", err)
	}

	return nil
}
