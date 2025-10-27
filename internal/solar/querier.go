package solar

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"time"
)

// Inverter telemetry that I care about
type Data struct {
	Timestamp time.Time `json:"timestamp"`

	ModelName           string  `json:"model_name"`
	SerialNumber        string  `json:"serial_number"`
	InternalTemperature float64 `json:"internal_temperature_c"`
	DeviceStatus        uint16  `json:"device_status"`
	DeviceStatusText    string  `json:"device_status_text"`

	// I believe this is DC input power?
	InputPowerW float64 `json:"input_power_w"`
	// ...whereas this is the inverted AC power
	ActivePowerW float64 `json:"active_power_w"`

	// AC bus as seen at the inverter
	// At night this just goes to 0
	GridVoltageV    float64 `json:"grid_voltage_v"`
	GridFrequencyHz float64 `json:"grid_frequency_hz"`

	// MPPT cumulative energy (kWh)
	// yes, funny word, but its consistent with others
	MPPT1CumKWh float64 `json:"mppt1_cum_kwh"`
	MPPT2CumKWh float64 `json:"mppt2_cum_kwh"`
	MPPT3CumKWh float64 `json:"mppt3_cum_kwh"`

	// PV string voltages and currents
	PV1VoltageV float64 `json:"pv1_voltage_v"`
	PV1CurrentA float64 `json:"pv1_current_a"`
	PV2VoltageV float64 `json:"pv2_voltage_v"`
	PV2CurrentA float64 `json:"pv2_current_a"`
	PV3VoltageV float64 `json:"pv3_voltage_v"`
	PV3CurrentA float64 `json:"pv3_current_a"`

	// Phase voltages, as read by the external meter, for single phase only A is used
	MeterGridAVoltageV float64 `json:"meter_grid_a_voltage_v"`
	MeterGridBVoltageV float64 `json:"meter_grid_b_voltage_v"`
	MeterGridCVoltageV float64 `json:"meter_grid_c_voltage_v"`
	MeterGridFrequency float64 `json:"meter_grid_frequency_hz"`

	// Power read by the external meter
	MeterActivePowerW     float64 `json:"meter_active_power_w"`
	MeterReactivePowerW   float64 `json:"meter_reactive_power_w"`
	MeterActiveGridPowerW float64 `json:"meter_active_grid_power_w"`

	// Power read within the inverter
	InverterActivePowerW   float64 `json:"inverter_active_power_w"`
	InverterReactivePowerW float64 `json:"inverter_reactive_power_w"`
}

func (c *Client) Query(_ context.Context) (*Data, error) {
	var err error

	d := &Data{Timestamp: time.Now().UTC()}

	// Identity
	if d.ModelName, err = c.readString(30000, 15); err != nil {
		return nil, fmt.Errorf("read model_name: %w", err)
	}
	if d.SerialNumber, err = c.readString(30015, 10); err != nil {
		return nil, fmt.Errorf("read serial_number: %w", err)
	}

	// Core inverter metrics
	if d.InputPowerW, err = c.readI32Scaled(32064, 1); err != nil {
		return nil, fmt.Errorf("read input_power: %w", err)
	}
	if d.GridVoltageV, err = c.readU16Scaled(32066, 10); err != nil {
		return nil, fmt.Errorf("read grid_voltage: %w", err)
	}
	if d.ActivePowerW, err = c.readI32Scaled(32080, 1); err != nil {
		return nil, fmt.Errorf("read active_power: %w", err)
	}
	if d.GridFrequencyHz, err = c.readU16Scaled(32085, 100); err != nil {
		return nil, fmt.Errorf("read grid_frequency: %w", err)
	}
	if d.InternalTemperature, err = c.readI16Scaled(32087, 10); err != nil {
		return nil, fmt.Errorf("read internal_temperature: %w", err)
	}
	if d.DeviceStatus, err = c.readU16(32089); err != nil {
		return nil, fmt.Errorf("read device_status: %w", err)
	}
	d.DeviceStatusText = StatusText(d.DeviceStatus)

	// MPPT cumulative energy (kWh)
	if d.MPPT1CumKWh, err = c.readU32Scaled(32212, 100); err != nil {
		return nil, fmt.Errorf("read mppt1_cum_kwh: %w", err)
	}
	if d.MPPT2CumKWh, err = c.readU32Scaled(32214, 100); err != nil {
		return nil, fmt.Errorf("read mppt2_cum_kwh: %w", err)
	}
	if d.MPPT3CumKWh, err = c.readU32Scaled(32216, 100); err != nil {
		return nil, fmt.Errorf("read mppt3_cum_kwh: %w", err)
	}

	// PV string measurements
	if d.PV1VoltageV, err = c.readI16Scaled(32016, 10); err != nil {
		return nil, fmt.Errorf("read pv1_voltage_v: %w", err)
	}
	if d.PV1CurrentA, err = c.readI16Scaled(32017, 100); err != nil {
		return nil, fmt.Errorf("read pv1_current_a: %w", err)
	}
	if d.PV2VoltageV, err = c.readI16Scaled(32018, 10); err != nil {
		return nil, fmt.Errorf("read pv2_voltage_v: %w", err)
	}
	if d.PV2CurrentA, err = c.readI16Scaled(32019, 100); err != nil {
		return nil, fmt.Errorf("read pv2_current_a: %w", err)
	}
	if d.PV3VoltageV, err = c.readI16Scaled(32020, 10); err != nil {
		return nil, fmt.Errorf("read pv3_voltage_v: %w", err)
	}
	if d.PV3CurrentA, err = c.readI16Scaled(32021, 100); err != nil {
		return nil, fmt.Errorf("read pv3_current_a: %w", err)
	}

	// Meter/grid voltages and powers
	if d.MeterGridAVoltageV, err = c.readI32Scaled(37101, 10); err != nil {
		return nil, fmt.Errorf("read meter_grid_a_voltage_v: %w", err)
	}
	if d.MeterGridBVoltageV, err = c.readI32Scaled(37103, 10); err != nil {
		return nil, fmt.Errorf("read meter_grid_b_voltage_v: %w", err)
	}
	if d.MeterGridCVoltageV, err = c.readI32Scaled(37105, 10); err != nil {
		return nil, fmt.Errorf("read meter_grid_c_voltage_v: %w", err)
	}
	if d.MeterActivePowerW, err = c.readI32Scaled(37113, 1); err != nil {
		return nil, fmt.Errorf("read meter_active_power_w: %w", err)
	}
	if d.MeterReactivePowerW, err = c.readI32Scaled(37115, 1); err != nil {
		return nil, fmt.Errorf("read meter_reactive_power_w: %w", err)
	}
	if d.MeterActiveGridPowerW, err = c.readI32Scaled(37132, 1); err != nil {
		return nil, fmt.Errorf("read meter_active_grid_power_w: %w", err)
	}
	if d.MeterGridFrequency, err = c.readI16Scaled(37118, 100); err != nil {
		return nil, fmt.Errorf("read meter_grid_frequency_hz: %w", err)
	}

	// Inverter power
	if d.InverterActivePowerW, err = c.readI32Scaled(32080, 1); err != nil {
		return nil, fmt.Errorf("read inverter_active_power_w: %w", err)
	}
	if d.InverterReactivePowerW, err = c.readI32Scaled(32082, 1); err != nil {
		return nil, fmt.Errorf("read inverter_reactive_power_w: %w", err)
	}

	return d, nil
}

func (d Data) Pretty() string {
	return fmt.Sprintf("%#v", d)
}

func (c *Client) readU16(addr uint16) (uint16, error) {
	b, err := c.client.ReadHoldingRegisters(addr, 1)
	if err != nil {
		return 0, err
	}
	if len(b) < 2 {
		return 0, fmt.Errorf("short read u16 at %d", addr)
	}
	return binary.BigEndian.Uint16(b[:2]), nil
}

func (c *Client) readU16Scaled(addr uint16, gain uint32) (float64, error) {
	v, err := c.readU16(addr)
	if err != nil {
		return 0, err
	}
	return float64(v) / float64(gain), nil
}

func (c *Client) readI16Scaled(addr uint16, gain uint32) (float64, error) {
	b, err := c.client.ReadHoldingRegisters(addr, 1)
	if err != nil {
		return 0, err
	}
	if len(b) < 2 {
		return 0, fmt.Errorf("short read i16 at %d", addr)
	}
	v := int16(binary.BigEndian.Uint16(b[:2]))
	return float64(v) / float64(gain), nil
}

func (c *Client) readI32Scaled(addr uint16, gain uint32) (float64, error) {
	b, err := c.client.ReadHoldingRegisters(addr, 2)
	if err != nil {
		return 0, err
	}
	if len(b) < 4 {
		return 0, fmt.Errorf("short read i32 at %d", addr)
	}
	v := int32(binary.BigEndian.Uint32(b[:4]))
	return float64(v) / float64(gain), nil
}

func (c *Client) readU32Scaled(addr uint16, gain uint32) (float64, error) {
	b, err := c.client.ReadHoldingRegisters(addr, 2)
	if err != nil {
		return 0, err
	}
	if len(b) < 4 {
		return 0, fmt.Errorf("short read i32 at %d", addr)
	}
	v := uint32(binary.BigEndian.Uint32(b[:4]))
	return float64(v) / float64(gain), nil
}

func (c *Client) readString(addr uint16, count uint16) (string, error) {
	b, err := c.client.ReadHoldingRegisters(addr, count)
	if err != nil {
		return "", err
	}
	// UTF-8/ASCII packed in big-endian u16 registers.
	// Remove trailing NULs.
	s := strings.TrimRight(string(b), "\x00")
	return s, nil
}

func StatusText(code uint16) string {
	if s, ok := deviceStatusDefinitions[code]; ok {
		return s
	}
	return "Unknown"
}

var deviceStatusDefinitions = map[uint16]string{
	0x0000: "Standby, initializing",
	0x0001: "Standby, detecting insulation resistance",
	0x0002: "Standby, detecting irradiation",
	0x0003: "Standby, grid detecting",
	0x0100: "Starting",
	0x0200: "On-grid",
	0x0201: "Grid Connection, power limited",
	0x0202: "Grid Connection, self-derating",
	0x0300: "Shutdown, fault",
	0x0301: "Shutdown, command",
	0x0302: "Shutdown, OVGR",
	0x0303: "Shutdown, communication disconnected",
	0x0304: "Shutdown, power limited",
	0x0305: "Shutdown, manual startup required",
	0x0306: "Shutdown, DC switches disconnected",
	0x0307: "Shutdown, rapid cutoff",
	0x0308: "Shutdown, input underpowered",
	0x0401: "Grid scheduling, cosphi-P curve",
	0x0402: "Grid scheduling, Q-U curve",
	0x0403: "Grid scheduling, PF-U curve",
	0x0404: "Grid scheduling, dry contact",
	0x0405: "Grid scheduling, Q-P curve",
	0x0500: "Spot-check ready",
	0x0501: "Spot-checking",
	0x0600: "Inspecting",
	0x0700: "AFCI self check",
	0x0800: "I-V scanning",
	0x0900: "DC input detection",
	0x0A00: "Running, off-grid charging",
	0xA000: "Standby, no irradiation",
}
