package solar

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/goburrow/modbus"
)

func (c *Client) loginHash(password string, challenge []byte) []byte {
	k := sha256.Sum256([]byte(password))
	mac := hmac.New(sha256.New, k[:])
	mac.Write(challenge)
	return mac.Sum(nil)
}

func (c *Client) loginInit() (*modbus.ProtocolDataUnit, error) {
	pduResp, err := c.doPDU(modbus.ProtocolDataUnit{
		FunctionCode: 0x41,
		Data: []byte{
			0x24, // login subcmd 1
			1,    // idk
			0,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("err doing PDU for login init: %v", err)
	}
	fmt.Printf("pduResp code=%x data=%v\n", pduResp.FunctionCode, pduResp.Data)
	return pduResp, nil
}

func (c *Client) loginInitialChallengeResponse(username string, challResp []byte) (*modbus.ProtocolDataUnit, error) {
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

	partTwoReq := modbus.ProtocolDataUnit{
		FunctionCode: 0x41,
		Data:         partTwoReqData,
	}

	fmt.Printf("sending part 2......%v\n", partTwoReq.Data)
	partTwoResp, err := c.doPDU(partTwoReq)
	if err != nil {
		return nil, fmt.Errorf("error on part 2 of login(data=%v): %v", partTwoResp, err)
	}
	return partTwoResp, nil
}

func (c *Client) Login(username string, password string) error {
	pduResp, err := c.loginInit()
	if err != nil {
		return err
	}
	respChall := pduResp.Data[2:18]

	firstChallResp := c.loginHash(password, respChall)

	fmt.Printf("first chal resp len: %d, %d\n\n", len(firstChallResp), byte(len(firstChallResp)))
	time.Sleep(time.Second)

	partTwoResp, err := c.loginInitialChallengeResponse(username, firstChallResp)
	if err != nil {
		return err
	}

	// response is.... 37, 36, 1, 32, ...... , <code>, 55
	// codes
	// 6: incorrect password...?
	// 38: incorrect password? or maybe account locked?
	// 2: invalid username?
	// hisolar sometimes says "user already logged in", so maybe that's one of those error codes?

	fmt.Printf("\np2 fc=%d, data=%v", partTwoResp.FunctionCode, partTwoResp.Data)
	return nil
}
