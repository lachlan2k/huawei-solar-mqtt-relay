package solar

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"time"

	"github.com/lachlan2k/huawei-solar-mqtt-relay/internal/modbus"
)

func loginHash(password string, challenge []byte) []byte {
	k := sha256.Sum256([]byte(password))
	mac := hmac.New(sha256.New, k[:])
	mac.Write(challenge)
	return mac.Sum(nil)
}

func (c *Client) loginInit(ctx context.Context) (*modbus.ModbusTCPADU, error) {
	slog.Debug("sending login init")
	resp, err := c.conn.FunctionCall(ctx, 0x41, []byte{
		0x24, // Login command part 1
		1,    // idk
		0,
	})

	if err != nil {
		return nil, fmt.Errorf("err doing PDU for login init: %v", err)
	}

	slog.Debug("login init response", "response", resp)

	return resp, nil
}

func (c *Client) loginInitialChallengeResponse(ctx context.Context, username string, challResp []byte) (*modbus.ModbusTCPADU, error) {
	partTwoReqData := []byte{
		0x25, // login subcmd 2

		byte(16 + 1 + len(username) + 1 + len(challResp)),

		// placeholder client challenge
		41, 42, 43, 44,
		45, 46, 47, 48,
		41, 42, 43, 44,
		45, 46, 47, 48,
	}

	partTwoReqData = append(partTwoReqData, byte(len(username)))
	partTwoReqData = append(partTwoReqData, []byte(username)...)

	partTwoReqData = append(partTwoReqData, byte(len(challResp)))
	partTwoReqData = append(partTwoReqData, []byte(challResp)...)

	slog.Debug("sending login challenge part two", "data", fmt.Sprintf("%v", partTwoReqData))
	partTwoResp, err := c.conn.FunctionCall(ctx, 0x41, partTwoReqData)
	if err != nil {
		return nil, fmt.Errorf("error on part 2 of login(data=%v): %v", partTwoResp, err)
	}
	slog.Debug("response to login challenge part two", "response", partTwoResp)

	return partTwoResp, nil
}

func (c *Client) Login(ctx context.Context, username string, password string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	slog.Info("logging in", "username", username)

	resp, err := c.loginInit(ctx)
	if err != nil {
		return err
	}

	if len(resp.Data) < 18 {
		return fmt.Errorf("invalid response data length: %d", len(resp.Data))
	}
	firstChallenge := resp.Data[2:18]

	challResponse := loginHash(password, firstChallenge)
	slog.Debug("responding to first challenge", "challenge", firstChallenge, "response", challResponse)
	time.Sleep(time.Second)

	partTwoResp, err := c.loginInitialChallengeResponse(ctx, username, challResponse)
	if err != nil {
		return err
	}

	slog.Debug("login part two response", "data", fmt.Sprintf("%v", partTwoResp.Data))

	// response is.... 37, 36, 1, 32, ...... , <code>, 55
	// codes
	// 6: incorrect password...?
	// 38: incorrect password? or maybe account locked?
	// 2: invalid username?
	// hisolar sometimes says "user already logged in", so maybe that's one of those error codes?

	return nil
}
