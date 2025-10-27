package modbus

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"reflect"
	"strconv"
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

func NewModbusConn(conn net.Conn, slaveId uint8) *ModbusConn {
	txId := atomic.Uint32{} // atomic doesn't give us u16. u32 will overflow during conversion and thats fine
	txId.Store(1234)

	return &ModbusConn{
		conn:    conn,
		txId:    &txId,
		slaveId: slaveId,

		aduRxCh: make(chan *ModbusTCPADU),
		aduTxCh: make(chan *ModbusTCPADU),
		waiters: make(map[uint16]chan *ModbusTCPADU),
	}
}

func (c *ModbusConn) Run(parentCtx context.Context) error {
	defer c.conn.Close()
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
		packet := &ModbusTCPADU{}
		err := packet.Scan(c.conn)
		if err != nil {
			return err
		}

		select {
		case c.aduRxCh <- packet:

		case <-ctx.Done():
			slog.Info("modbus receiver context finished")
			return ctx.Err()
		}
	}
}

func (c *ModbusConn) transmitter(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			slog.Info("modbus transmitter context finished")
			return ctx.Err()

		case packet := <-c.aduTxCh:
			b := packet.Marshal()
			slog.Debug("sending packet", "transaction_id", packet.TransactionID, "function_code", packet.FunctionCode)
			_, err := c.conn.Write(b)
			if err != nil {
				return err
			}
		}
	}
}

func (c *ModbusConn) fanout(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			slog.Info("modbus fanout context finished")
			return ctx.Err()

		case packet := <-c.aduRxCh:
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

	slog.Debug("sending modbus function call", "transaction_id", transactionID, "function_code", fc, "data", fmt.Sprintf("%v", data))
	resultCh := c.waiter(transactionID)

	select {
	case c.aduTxCh <- req:

	case <-ctx.Done():
		return nil, fmt.Errorf("modbus waiting to send call: %v", ctx.Err())
	}

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("modbus waiting to receive response: %v", ctx.Err())

	case result := <-resultCh:
		return result, nil
	}
}

func (c *ModbusConn) ReadHoldingRegistersU16(ctx context.Context, address, quantity uint16) ([]byte, error) {
	if quantity < 1 || quantity > 125 {
		return nil, fmt.Errorf("modbus: quantity '%v' must be between '%v' and '%v',", quantity, 1, 125)
	}

	var buff bytes.Buffer

	binary.Write(&buff, binary.BigEndian, address)
	binary.Write(&buff, binary.BigEndian, quantity)

	resp, err := c.FunctionCall(ctx, 0x03, buff.Bytes())
	if err != nil {
		return nil, fmt.Errorf("modbus: failed to make call to read holding registers: %v", err)
	}

	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("modbus: register read response data is empty")
	}

	count := uint16(resp.Data[0])
	if count != quantity*2 {
		return nil, fmt.Errorf("modbus: response data size '%d' does not match requested '%d' registers", count, quantity*2)
	}

	values := resp.Data[1:]

	if int(count) != len(values) {
		return nil, fmt.Errorf("modbus: response data payload size '%d' does not match expected '%d'", count, len(resp.Data)-2)
	}

	return values, nil
}

func ReadHoldingRegisters[T constraints.Integer | constraints.Float](c *ModbusConn, ctx context.Context, address, quantityT uint16) ([]T, error) {
	tSize := intDataSize(T(0))
	quantityU16 := uint16(math.Ceil(float64(quantityT) * float64(tSize) / 2))

	if quantityU16 > 125 {
		return nil, fmt.Errorf("modbus: reading %d values results in %d u16 registesr, which is more than 125", quantityT, quantityU16)
	}

	slog.Debug("querying modbus holding registers", "address", address, "quantity", quantityT, "total", quantityU16)
	valuesAsBytes, err := c.ReadHoldingRegistersU16(ctx, address, quantityU16)
	if err != nil {
		return nil, err
	}

	results := make([]T, quantityT)
	for i := range results {
		this := i * tSize
		next := (i + 1) * tSize
		binary.Decode(valuesAsBytes[this:next], binary.BigEndian, &results[i])
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
func ReadHoldingRegisterP[T constraints.Integer | constraints.Float](c *ModbusConn, ctx context.Context, address uint16, result *T) error {
	res, err := ReadHoldingRegisters[T](c, ctx, address, 1)
	if err != nil {
		return err
	}
	*result = res[0]
	return nil
}

func ReadHoldingRegisterString(c *ModbusConn, ctx context.Context, address uint16, size uint16) (string, error) {
	// +! null terminator
	res, err := ReadHoldingRegisters[byte](c, ctx, address, size+1)
	if err != nil {
		return "", err
	}

	return strings.TrimRight(string(res[:size]), "\x00"), nil
}

func ReadHoldingRegisterAny(c *ModbusConn, ctx context.Context, address uint16, result any) error {
	switch v := result.(type) {
	case *int8:
		return ReadHoldingRegisterP(c, ctx, address, v)
	case *uint8:
		return ReadHoldingRegisterP(c, ctx, address, v)
	case *int16:
		return ReadHoldingRegisterP(c, ctx, address, v)
	case *uint16:
		return ReadHoldingRegisterP(c, ctx, address, v)
	case *int32:
		return ReadHoldingRegisterP(c, ctx, address, v)
	case *uint32:
		return ReadHoldingRegisterP(c, ctx, address, v)
	case *int64:
		return ReadHoldingRegisterP(c, ctx, address, v)
	case *uint64:
		return ReadHoldingRegisterP(c, ctx, address, v)
	case *float32:
		return ReadHoldingRegisterP(c, ctx, address, v)
	case *float64:
		return ReadHoldingRegisterP(c, ctx, address, v)
	}
	return fmt.Errorf("modbus unsupported type for 'any' read %T", result)
}

func (c *ModbusConn) QueryStructRegisters(ctx context.Context, d interface{}) error {
	v := reflect.ValueOf(d).Elem()
	st := v.Type()

	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		fieldType := st.Field(i)

		addrTag := fieldType.Tag.Get("modbus_addr")
		if addrTag == "" {
			continue
		}

		addr, err := strconv.ParseUint(addrTag, 10, 16)
		if err != nil {
			return fmt.Errorf("field %q has an invalid modbus_addr tag: %v", fieldType.Name, err)
		}

		switch field.Type().Kind() {
		case reflect.String:
			strLenStr := fieldType.Tag.Get("modbus_str_len")
			strLen, err := strconv.ParseInt(strLenStr, 10, 16)

			if err != nil || strLen == 0 {
				return fmt.Errorf("field %q is a string, but does not have a valid modbus_str_len tag", fieldType.Name)
			}

			strOut, err := ReadHoldingRegisterString(c, ctx, uint16(addr), uint16(strLen))
			if err != nil {
				return fmt.Errorf("failed to read %q (%s): %v", fieldType.Name, fieldType.Type.Name(), err)
			}
			field.SetString(strOut)

		default:
			err := ReadHoldingRegisterAny(c, ctx, uint16(addr), field.Addr().Interface())
			if err != nil {
				return fmt.Errorf("failed to read %q (%s): %v", fieldType.Name, fieldType.Type.Name(), err)
			}
		}
	}

	return nil
}

// from encoding/binary, but removed slice types, and made it generic
func intDataSize[T constraints.Integer | constraints.Float](data T) int {
	switch any(data).(type) {
	case int8, uint8:
		return 1
	case int16, uint16:
		return 2
	case int32, uint32, float32:
		return 4
	case int64, uint64, float64:
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
