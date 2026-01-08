package modbus

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"math"
	"reflect"
	"strconv"
	"strings"
)

type numeric interface {
	int8 | int16 | int32 | int64 |
		uint8 | uint16 | uint32 | uint64 |
		float32 | float64
}

func sizeOf[T numeric]() int {
	switch any(T(0)).(type) {
	case int8, uint8:
		return 1
	case int16, uint16:
		return 2
	case int32, uint32, float32:
		return 4
	case int64, uint64, float64:
		return 8
	}
	panic("unreachable")
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

func ReadHoldingRegisters[T numeric](c *ModbusConn, ctx context.Context, address, quantityT uint16) ([]T, error) {
	tSize := sizeOf[T]()
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

func ReadHoldingRegister[T numeric](c *ModbusConn, ctx context.Context, address uint16) (T, error) {
	res, err := ReadHoldingRegisters[T](c, ctx, address, 1)
	if err != nil {
		return T(0), err
	}
	return res[0], nil
}
func ReadHoldingRegisterP[T numeric](c *ModbusConn, ctx context.Context, address uint16, result *T) error {
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

		scalarStr := fieldType.Tag.Get("modbus_scalar")
		if scalarStr == "" {
			scalarStr = "1"
		}
		scalar, err := strconv.ParseFloat(scalarStr, 64)
		if err != nil {
			return fmt.Errorf("field %q has an invalid modbus_scalar tag: %v", fieldType.Name, err)
		}
		// note, the listed scalar was what the *original* scalar was
		// i.e., "230.1" stored as "2301" has a scalar of 10
		// so we *divide* by said scalar
		scalar = 1.0 / scalar

		outputType := fieldType.Tag.Get("modbus_type")
		if outputType == "" {
			outputType = fieldType.Type.Name()
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
			result := anyNumByName(outputType)
			err := ReadHoldingRegisterAny(c, ctx, uint16(addr), result)
			if err != nil {
				return fmt.Errorf("failed to read %q (%s): %v", fieldType.Name, fieldType.Type.Name(), err)
			}

			if field.CanInt() {
				field.SetInt(castAnyNumTo[int64](result) * int64(scalar))
			} else if field.CanFloat() {
				field.SetFloat(castAnyNumTo[float64](result) * scalar)
			} else if field.CanUint() {
				field.SetUint(castAnyNumTo[uint64](result) * uint64(scalar))
			} else {
				return fmt.Errorf("can't set field %q (%s), its neither int, float, nor uint", fieldType.Name, fieldType.Type.Name())
			}
		}
	}

	return nil
}

func castAnyNumTo[OutT numeric](num any) OutT {
	switch num := num.(type) {
	case *int8:
		return OutT(*num)
	case *uint8:
		return OutT(*num)
	case *int16:
		return OutT(*num)
	case *uint16:
		return OutT(*num)
	case *int32:
		return OutT(*num)
	case *uint32:
		return OutT(*num)
	case *int64:
		return OutT(*num)
	case *uint64:
		return OutT(*num)
	case *float32:
		return OutT(*num)
	case *float64:
		return OutT(*num)
	}
	panic("unknown type of number")
}

func anyNumByName(name string) any {
	switch name {
	case "int8", "i8":
		return new(int8)
	case "uint8", "u8":
		return new(uint8)
	case "int16", "i16":
		return new(int16)
	case "uint16", "u16":
		return new(uint16)
	case "int32", "i32":
		return new(int32)
	case "uint32", "u32":
		return new(uint32)
	case "int64", "i64":
		return new(int64)
	case "uint64", "u64":
		return new(uint64)
	case "float32", "f32":
		return new(float32)
	case "float64", "f64":
		return new(float64)
	}
	panic("unknown type of number: " + name)
}
