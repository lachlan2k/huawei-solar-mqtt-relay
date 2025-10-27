package modbus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

type ModbusMBAPHeader struct {
	TransactionID uint16
	ProtocolID    uint16
	Length        uint16
	UnitID        uint8
}

func (h *ModbusMBAPHeader) Scan(r io.Reader) error {
	header := make([]byte, 7)
	_, err := io.ReadFull(r, header)
	if err != nil {
		return fmt.Errorf("failed to read header: %v", err)
	}

	headerR := bytes.NewReader(header)

	binary.Read(headerR, binary.BigEndian, &h.TransactionID)
	binary.Read(headerR, binary.BigEndian, &h.ProtocolID)
	binary.Read(headerR, binary.BigEndian, &h.Length)
	binary.Read(headerR, binary.BigEndian, &h.UnitID)

	if h.ProtocolID != 0 {
		return fmt.Errorf("invalid protocol id: %d", h.ProtocolID)
	}
	if h.Length < 2 {
		return fmt.Errorf("invalid length: %d", h.Length)
	}

	return nil
}

func (h *ModbusMBAPHeader) Marshal() []byte {
	buf := new(bytes.Buffer)

	binary.Write(buf, binary.BigEndian, h.TransactionID)
	binary.Write(buf, binary.BigEndian, h.ProtocolID)
	binary.Write(buf, binary.BigEndian, h.Length)
	binary.Write(buf, binary.BigEndian, h.UnitID)

	return buf.Bytes()
}

type ModbusTCPADU struct {
	ModbusMBAPHeader

	FunctionCode uint8
	Data         []byte
}

func (h *ModbusTCPADU) Scan(r io.Reader) error {
	err := h.ModbusMBAPHeader.Scan(r)
	if err != nil {
		return err
	}

	err = binary.Read(r, binary.BigEndian, &h.FunctionCode)
	if err != nil {
		return fmt.Errorf("failed to read function code: %v", err)
	}

	h.Data = make([]byte, h.Length-2) // -2 for unit id + fc (already read)
	_, err = io.ReadFull(r, h.Data)

	if err != nil {
		return fmt.Errorf("failed to read data: %v", err)
	}
	return nil
}

func (h *ModbusTCPADU) Unmarshal(b []byte) error {
	return h.Scan(bytes.NewReader(b))
}

func (h *ModbusTCPADU) Marshal() []byte {
	buf := new(bytes.Buffer)

	buf.Write(h.ModbusMBAPHeader.Marshal())
	buf.WriteByte(h.FunctionCode)
	buf.Write(h.Data)

	return buf.Bytes()
}
