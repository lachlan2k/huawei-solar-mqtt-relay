package solar

import (
	"context"
	"fmt"
	"time"
)

// Inverter telemetry that I care about
type Data struct {
	Timestamp time.Time `json:"timestamp"`

	ModelName           string  `json:"model_name" modbus_addr:"30000" modbus_str_len:"30"`
	SerialNumber        string  `json:"serial_number" modbus_addr:"30015" modbus_str_len:"20"`
	InternalTemperature float64 `json:"internal_temperature_c" modbus_type:"i16" modbus_scalar:"10" modbus_addr:"32087"`
	DeviceStatus        uint16  `json:"device_status" modbus_addr:"32089"`
	DeviceStatusText    string  `json:"device_status_text"`

	// I believe this is DC input power?
	InputPowerW float64 `json:"input_power_w" modbus_type:"i32" modbus_scalar:"1" modbus_addr:"32064"`
	// ...whereas this is the inverted AC power
	ActivePowerW float64 `json:"active_power_w" modbus_type:"i32" modbus_scalar:"1" modbus_addr:"32080"`

	// AC bus as seen at the inverter
	// At night this just goes to 0
	GridVoltageV    float64 `json:"grid_voltage_v" modbus_type:"u16" modbus_scalar:"10" modbus_addr:"32066"`
	GridFrequencyHz float64 `json:"grid_frequency_hz" modbus_type:"u16" modbus_scalar:"100" modbus_addr:"32085"`

	// MPPT cumulative energy (kWh)
	// yes, funny word, but its consistent with others
	MPPT1CumKWh float64 `json:"mppt1_cum_kwh" modbus_type:"u32" modbus_scalar:"100" modbus_addr:"32212"`
	MPPT2CumKWh float64 `json:"mppt2_cum_kwh" modbus_type:"u32" modbus_scalar:"100" modbus_addr:"32214"`
	MPPT3CumKWh float64 `json:"mppt3_cum_kwh" modbus_type:"u32" modbus_scalar:"100" modbus_addr:"32216"`

	// PV string voltages and currents
	PV1VoltageV float64 `json:"pv1_voltage_v" modbus_type:"i16" modbus_scalar:"10" modbus_addr:"32016"`
	PV1CurrentA float64 `json:"pv1_current_a" modbus_type:"i16" modbus_scalar:"100" modbus_addr:"32017"`
	PV2VoltageV float64 `json:"pv2_voltage_v" modbus_type:"i16" modbus_scalar:"10" modbus_addr:"32018"`
	PV2CurrentA float64 `json:"pv2_current_a" modbus_type:"i16" modbus_scalar:"100" modbus_addr:"32019"`
	PV3VoltageV float64 `json:"pv3_voltage_v" modbus_type:"i16" modbus_scalar:"10" modbus_addr:"32020"`
	PV3CurrentA float64 `json:"pv3_current_a" modbus_type:"i16" modbus_scalar:"100" modbus_addr:"32021"`

	// Phase voltages, as read by the external meter, for single phase only A is used
	MeterGridAVoltageV float64 `json:"meter_grid_a_voltage_v" modbus_type:"i32" modbus_scalar:"10" modbus_addr:"37101"`
	MeterGridBVoltageV float64 `json:"meter_grid_b_voltage_v" modbus_type:"i32" modbus_scalar:"10" modbus_addr:"37103"`
	MeterGridCVoltageV float64 `json:"meter_grid_c_voltage_v" modbus_type:"i32" modbus_scalar:"10" modbus_addr:"37105"`
	MeterGridFrequency float64 `json:"meter_grid_frequency_hz" modbus_type:"i16" modbus_scalar:"100" modbus_addr:"37118"`

	// Power read by the external meter
	MeterActivePowerW     float64 `json:"meter_active_power_w" modbus_type:"i32" modbus_scalar:"1" modbus_addr:"37113"`
	MeterReactivePowerW   float64 `json:"meter_reactive_power_w" modbus_type:"i32" modbus_scalar:"1" modbus_addr:"37115"`
	MeterActiveGridPowerW float64 `json:"meter_active_grid_power_w" modbus_type:"i32" modbus_scalar:"1" modbus_addr:"37132"`

	// Power read within the inverter
	InverterActivePowerW   float64 `json:"inverter_active_power_w" modbus_type:"i32" modbus_scalar:"1" modbus_addr:"32080"`
	InverterReactivePowerW float64 `json:"inverter_reactive_power_w" modbus_type:"i32" modbus_scalar:"1" modbus_addr:"32082"`
}

func (c *Client) Query(ctx context.Context) (*Data, error) {
	d := &Data{Timestamp: time.Now().UTC()}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	err := c.conn.QueryStructRegisters(ctx, d)
	if err != nil {
		return nil, err
	}

	d.DeviceStatusText = StatusText(d.DeviceStatus)
	return d, nil
}

func (d Data) Pretty() string {
	return fmt.Sprintf("%#v", d)
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
