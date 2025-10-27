# Huawei Solar Inverter to MQTT Relay

For Huawei SUN2000 inverters, or equivalents (iStore, Entelar, etc.). Tested on an Entelar EESOLAR-10KTL-LC0 (SUN2000).

Connect to your inverter, and poll statistics like power, status, string voltages, etc. and publish them to MQTT.

## Config

Example in `config.example.yaml`.

### "Modbus" section

- Set the IP of the inverter. The port is likely `6607`, `6606` or `502`
- Slave ID of `1` works for me, connecting directly to the inverter (no smart dongle).
- Username/password can either be for `installer` or `user`.

### "Broadcast" section

**Important:** Newer inverters don't allow you to connect until you've sent a 'hello' broadcast discovery packet.

1. If you run the agent on the same subnet/VLAN as the inverter:
    - Set `broadcast.destination_ip` to `255.255.255.255`.
    - Set `broadcast.self_ip` to the IP of the machine running the program.
2. If the inverter's on a different subnet/VLAN, you'll need to:
    - Set `broadcast.destination_ip` to the broadcast address of the inverter's subnet. I.e. if your inverter is on 192.168.8.0/24, then the broadcast address is 192.168.8.255.
    - Set `broadcast.self_ip` to the IP of the machine running the program;
    - OR, if you have SNAT between the two subnets, `self_ip` should be the IP of your router on the inverter's subnet.

## Running it

### Docker

```bash
docker run -d --net host --name solar-mqtt-relay -v /path/to/config.yaml:/config/config.yaml ghcr.io/lachlan2k/huawei-solar-mqtt-relay:latest
```

### From source

```bash
go build -o solar-mqtt-relay .
./solar-mqtt-relay -config config.yaml
```