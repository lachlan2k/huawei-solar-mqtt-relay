package modbus

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"

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

	runningMu sync.Mutex
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

func (c *ModbusConn) Close() error {
	return c.conn.Close()
}

func (c *ModbusConn) SetConn(conn net.Conn) {
	c.conn = conn
}

func (c *ModbusConn) Run(parentCtx context.Context) error {
	ok := c.runningMu.TryLock()
	if !ok {
		return fmt.Errorf("modbus: already running")
	}
	defer c.runningMu.Unlock()

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
