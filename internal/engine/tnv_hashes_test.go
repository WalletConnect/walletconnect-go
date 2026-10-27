package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// Vectors come from packages/utils/test/signatures.spec.ts and canton.spec.ts
// in the JS monorepo, so both SDKs report the same hash for the same result.

const (
	solanaTx      = "AeJw688VKMWEeOHsYhe03By/2rqJHTQeq6W4L1ZLdbT2l/Nim8ctL3erMyH9IWPsQP73uaarRmiVfanEJHx7uQ4BAAIDb3ObYkq6BFd46JrMFy1h0Q+dGmyRGtpelqTKkIg82isAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAMGRm/lIRcy/+ytunLDm+e8jOW7xfcSayxDmzpAAAAAtIy17v5fs39LuoitzpBhVrg8ZIQF/3ih1N9dQ+X3shEDAgAFAlgCAAABAgAADAIAAACghgEAAAAAAAIACQMjTgAAAAAAAA=="
	solanaTxID    = "5XanD5KnkqzH3RjyqHzPCSRrNXYW2ADH4bge4oMi9KnDBrkFvugagH3LytFZFmBhZEEcyxPsZqeyF4cgLpEXVFR7"
	solanaBadTx   = "+dGmyRGtpelqTKkIg82isAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAMGRm/lIRcy/+ytunLDm+e8jOW7xfcSayxDmzpAAAAAtIy17v5fs39LuoitzpBhVrg8ZIQF/3ih1N9dQ+X3shEDAgAFAlgCAAABAgAADAIAAACghgEAAAAAAAIACQMjTgAAAAAAAA=="
	suiTx         = "AAACAAhkAAAAAAAAAAAg1fZH7bd9T9ox0DBFBkR/s8kuVar3e8XtS3fDMt1GBfoCAgABAQAAAQEDAAAAAAEBANX2R+23fU/aMdAwRQZEf7PJLlWq93vF7Ut3wzLdRgX6At/pRJzj2VpZgqXpSvEtd3GzPvt99hR8e/yOCGz/8nbRmA7QFAAAAAAgBy5vStJizn76LmJTBlDiONdR/2rSuzzS4L+Tp/Zs4hZ8cBxYkcSlxBD6QXvgS11E6d+DNek8LiA/beba6iH3l5gO0BQAAAAAIMpdmZjiqJ5GG9di1MAgD4S3uRr2gaMC7S1WsaeBwNIx1fZH7bd9T9ox0DBFBkR/s8kuVar3e8XtS3fDMt1GBfroAwAAAAAAAECrPAAAAAAAAA=="
	suiTxDigest   = "C98G1Uwh5soPMtZZmjUFwbVzWLMoAHzi5jrX2BtABe8v"
	nearTxID      = "EpHx79wKAn6br4G9aKaCGLpdzNc8YjrthiFonXQgskAx"
	algorandTx    = "gqNzaWfEQNGPgbxS9pTu0sTikT3cJVO48WFltc8MM8meFR+aAnGwOo3FO+0nFkAludT0jNqHRM6E65gW6k/m92sHVCxVnQWjdHhuiaNhbXTOAAehIKNmZWXNA+iiZnbOAv0CO6NnZW6sbWFpbm5ldC12MS4womdoxCDAYcTY/B293tLXYEvkVo4/bQQZh6w3veS2ILWrOSSK36Jsds4C/QYjo3JjdsQgeqRNTBEXudHx2kO9Btq289aRzj5DlNUw0jwX9KEnaZqjc25kxCDH1s5tvgARbjtHceUG07Sj5IDfqzn7Zwx0P+XuvCYMz6R0eXBlo3BheQ=="
	algorandTxID  = "OM5JS3AE4HVAT5ZMCIMY32HPD6KJAQVPFS2LL2ZW2R5JKUKZFVNA"
	cosmosTxHash  = "A7284BA475C55983E5BCB7D52F5C82CBFF19FD75725F5E0E33BA4384FCFC6052"
	polkadotHash  = "0x48016b3c80b7b61d32d1db6f52038de70d7d30ef948da047442cc9c952b92e84"
	stellarBadXDR = "q83vASNFZ4mrze8BI0VniavN7wEjRWeJq83vAQ=="
)

var nearTxBytes = []int{16, 0, 0, 0, 48, 120, 103, 97, 110, 99, 104, 111, 46, 116, 101, 115, 116, 110, 101, 116, 0, 243, 74, 204, 31, 29, 80, 146, 149, 102, 175, 8, 83, 231, 187, 5, 120, 41, 115, 247, 22, 197, 120, 182, 242, 120, 135, 73, 137, 166, 246, 171, 103, 77, 243, 34, 42, 212, 180, 0, 0, 16, 0, 0, 0, 48, 120, 103, 97, 110, 99, 104, 111, 46, 116, 101, 115, 116, 110, 101, 116, 5, 233, 95, 227, 45, 10, 101, 176, 111, 124, 190, 86, 106, 27, 143, 54, 148, 125, 132, 252, 25, 71, 125, 78, 60, 242, 100, 219, 40, 168, 65, 3, 1, 0, 0, 0, 3, 0, 0, 0, 161, 237, 204, 206, 27, 194, 211, 0, 0, 0, 0, 0, 0}

const polkadotParams = `{"transactionPayload": {"method": "050300c07d211d3c181df768d9d9d41df6f14f9d116d9c1906f38153b208259c315b4b02286bee", "specVersion": "c9550f00", "transactionVersion": "1a000000", "genesisHash": "91b171bb158e2d3848fa23a9f1c25182fb8e20313b2c1eb49219da7a70ce90c3", "blockHash": "130d8c27af0e0adfa85d370af89746f229780b677c81da97d11a4921cdb86df5", "era": "1502", "nonce": "1c", "tip": "00", "mode": "00", "metadataHash": "00", "address": "15JBFhDp1rQycRFuCtkr2VouMiWyDzh3qRUPA8STY53mdRmM", "version": 4}}`

const polkadotResult = `{"signature": "362cef5dff66aee851a5d8c5100a53590eddd7c75c1a53553b08861fb28ce80b96d53279f52a27c866639954c5efa32b52c148fefe78dbdad1f9d3be4f44538f"}`

const cosmosResult = `{"signature": {"pub_key": {"type": "tendermint/PubKeySecp256k1", "value": "AgSEjOuOr991QlHCORRmdE5ahVKeyBrmtgoYepCpQGOW"}, "signature": "S7BJEbiXQ6vxvF9o4Wj7qAcocMQqBsqz+NVH4wilhidFsRpyqpSP5XiXoQZxTDrT9uET/S5SH6+5gUmjYntH/Q=="}, "signed": {"chainId": "cosmoshub-4", "accountNumber": "1", "authInfoBytes": "ClAKRgofL2Nvc21vcy5jcnlwdG8uc2VjcDI1NmsxLlB1YktleRIjCiEDNOXj4u60JFq00+VbLBCNBTYy76Pn864AvYNFG/9cQwMSBAoCCH8YAhITCg0KBXVhdG9tEgQ0NTM1EIaJCw==", "bodyBytes": "CpoICikvaWJjLmFwcGxpY2F0aW9ucy50cmFuc2Zlci52MS5Nc2dUcmFuc2ZlchLsBwoIdHJhbnNmZXISC2NoYW5uZWwtMTQxGg8KBXVhdG9tEgYxODg4MDYiLWNvc21vczFhanBkZndsZmRqY240MG5yZXN5ZHJxazRhOGo2ZG0wemY0MGszcSo/b3NtbzEwYTNrNGh2azM3Y2M0aG54Y3R3NHA5NWZoc2NkMno2aDJybXgwYXVrYzZybTh1OXFxeDlzbWZzaDd1MgcIARDFt5YRQsgGeyJ3YXNtIjp7ImNvbnRyYWN0Ijoib3NtbzEwYTNrNGh2azM3Y2M0aG54Y3R3NHA5NWZoc2NkMno2aDJybXgwYXVrYzZybTh1OXFxeDlzbWZzaDd1IiwibXNnIjp7InN3YXBfYW5kX2FjdGlvbiI6eyJ1c2VyX3N3YXAiOnsic3dhcF9leGFjdF9hc3NldF9pbiI6eyJzd2FwX3ZlbnVlX25hbWUiOiJvc21vc2lzLXBvb2xtYW5hZ2VyIiwib3BlcmF0aW9ucyI6W3sicG9vbCI6IjE0MDAiLCJkZW5vbV9pbiI6ImliYy8yNzM5NEZCMDkyRDJFQ0NENTYxMjNDNzRGMzZFNEMxRjkyNjAwMUNFQURBOUNBOTdFQTYyMkIyNUY0MUU1RUIyIiwiZGVub21fb3V0IjoidW9zbW8ifSx7InBvb2wiOiIxMzQ3IiwiZGVub21faW4iOiJ1b3NtbyIsImRlbm9tX291dCI6ImliYy9ENzlFN0Q4M0FCMzk5QkZGRjkzNDMzRTU0RkFBNDgwQzE5MTI0OEZDNTU2OTI0QTJBODM1MUFFMjYzOEIzODc3In1dfX0sIm1pbl9hc3NldCI6eyJuYXRpdmUiOnsiZGVub20iOiJpYmMvRDc5RTdEODNBQjM5OUJGRkY5MzQzM0U1NEZBQTQ4MEMxOTEyNDhGQzU1NjkyNEEyQTgzNTFBRTI2MzhCMzg3NyIsImFtb3VudCI6IjMzOTQ2NyJ9fSwidGltZW91dF90aW1lc3RhbXAiOjE3NDc3NDY3MzM3OTU4OTgzNjQsInBvc3Rfc3dhcF9hY3Rpb24iOnsiaWJjX3RyYW5zZmVyIjp7ImliY19pbmZvIjp7InNvdXJjZV9jaGFubmVsIjoiY2hhbm5lbC02OTk0IiwicmVjZWl2ZXIiOiJjZWxlc3RpYTFhanBkZndsZmRqY240MG5yZXN5ZHJxazRhOGo2ZG0wemNsN3h0ZCIsIm1lbW8iOiIiLCJyZWNvdmVyX2FkZHJlc3MiOiJvc21vMWFqcGRmd2xmZGpjbjQwbnJlc3lkcnFrNGE4ajZkbTB6cHd1eDhqIn19fSwiYWZmaWxpYXRlcyI6W119fX19"}}`

var stellarVectors = []struct{ name, chain, xdr, hash string }{
	{"signed stellar V1 envelope (pubnet)", "stellar:pubnet", "AAAAAgAAAAAEJr/kOejtZoeowwp8zwNy2Q5PJ4BGTWfjsi8nOzNmrwABOIQDtAXqAA5+nQAAAAEAAAAAAAAAAAAAAABqgtc9AAAAAAAAAAQAAAAAAAAADAAAAAFVU0RDAAAAADuZETgO/piLoKiQDrHP5E82b32+lGvtB3JA9/Yk3xXFAAAAAXlYTE0AAAAAIjbXcP4NPgFSGXXVz3rEhCtwldaxqddo0+mmMumZBr4AAAAAWC0vBxJeEy11BinxAAAAAG57ynoAAAAAAAAADAAAAAFVU0RDAAAAADuZETgO/piLoKiQDrHP5E82b32+lGvtB3JA9/Yk3xXFAAAAAXlYTE0AAAAAIjbXcP4NPgFSGXXVz3rEhCtwldaxqddo0+mmMumZBr4AAAACy0F4AAGqIxIKms6JAAAAAG57nJcAAAAAAAAAAwAAAAF5WExNAAAAACI213D+DT4BUhl11c96xIQrcJXWsanXaNPppjLpmQa+AAAAAVVTREMAAAAAO5kROA7+mIugqJAOsc/kTzZvfb6Ua+0HckD39iTfFcUAAAAAstBeAA36DJRYpIptAAAAAG52hUIAAAAAAAAAAwAAAAF5WExNAAAAACI213D+DT4BUhl11c96xIQrcJXWsanXaNPppjLpmQa+AAAAAVVTREMAAAAAO5kROA7+mIugqJAOsc/kTzZvfb6Ua+0HckD39iTfFcUAAAACy0F4AA0hty1TaHPUAAAAAG56kgAAAAAAAAAAATszZq8AAABAxLKQz2a6NY74WUVxGbWvafiKXhExlAIu2WM2MKUNOidwRDZb3EDY/JP2TbM5rWXlmwZGKHE8iLYs19u/n2LgAA==", "f988eec2a159fa6031ef519bf4730dd50e86eb61f692334292d2d2b40d1465f1"},
	{"fee-bump envelope (pubnet)", "stellar:pubnet", "AAAABQAAAAA0mMHF8QGzwsMRBhe9i8PSIqxjNjKyQMyXZODBAdhAUwAAAAAAA5+BAAAAAgAAAAA6Hd6p+AA5GTO2bJqKN/hbBfWcOh2Ow8cnTY3iIFFVPAADnrgDoCvmAAAZVgAAAAEAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAEAAAABAAAAADod3qn4ADkZM7Zsmoo3+FsF9Zw6HY7DxydNjeIgUVU8AAAAGAAAAAAAAAAB1/5EvQrxHWArEJHy9KH03yEtRE0DIeoyrbPMHLurCgQAAAAEd29yawAAAAMAAAASAAAAAAAAAAA6Hd6p+AA5GTO2bJqKN/hbBfWcOh2Ow8cnTY3iIFFVPAAAAA0AAAAgAAAAjpSFVoEyx/Ev5d2V/desEr+GEqMyXKvrs5wXFqwAAAAFAAAAAAIrSjwAAAAAAAAAAQAAAAAAAAABAAAAB9ssFCkNSWTjgF8lJ90TKTm6X7P8ysVrML+rj9CRARYnAAAAAwAAAAYAAAAB1/5EvQrxHWArEJHy9KH03yEtRE0DIeoyrbPMHLurCgQAAAAQAAAAAQAAAAIAAAAPAAAABUJsb2NrAAAAAAAAAwACsj8AAAAAAAAABgAAAAHX/kS9CvEdYCsQkfL0ofTfIS1ETQMh6jKts8wcu6sKBAAAABAAAAABAAAAAwAAAA8AAAAEUGFpbAAAABIAAAAAAAAAADod3qn4ADkZM7Zsmoo3+FsF9Zw6HY7DxydNjeIgUVU8AAAAAwACsj8AAAAAAAAABgAAAAHX/kS9CvEdYCsQkfL0ofTfIS1ETQMh6jKts8wcu6sKBAAAABQAAAABABvFygAAAAAAAAXIAAAAAAADnrgAAAABIFFVPAAAAEBbcalx54eMMHwWJz7tzgOoxIVmMl4pexbgwTLzxnyMtAhZ2nZlsF18jIMDBaubSNSNPi4YRHkSajpyOcdZOzsCAAAAAAAAAAEB2EBTAAAAQDOlpIeUB73BImAZJCSAt0cuKZXHlKG+TJ+j+fCeFe9bDc5wHzMlDjU6YlB6geCuxRi7QwTq4RgxrOIJ9HICfg8=", "5906453d5a367b4a8a1af9bbbc934904841718ec1ca1345874904e15f97bf83b"},
	{"signed stellar V1 envelope (testnet)", "stellar:testnet", "AAAAAgAAAAAJDqKNhO2/XZAvmR1Wynm2lxfIUQwB6TDzqNVOlQgQTgAAm74AJGh0ABKiOwAAAAEAAAAAAAAAAAAAAABqgthtAAAAAAAAAAEAAAAAAAAAGAAAAAAAAAABmtIg9IzJFxPz3yuidCMj3LiE2SB3XYOl8DvtohHRPVAAAAAJc2V0X3ByaWNlAAAAAAAAAwAAABIAAAAAAAAAAAkOoo2E7b9dkC+ZHVbKebaXF8hRDAHpMPOo1U6VCBBOAAAADwAAAAZFVEhVU0QAAAAAAAoAAAAAAAAAAAAAAARomVswAAAAAQAAAAAAAAAAAAAAAZrSIPSMyRcT898ronQjI9y4hNkgd12DpfA77aIR0T1QAAAACXNldF9wcmljZQAAAAAAAAMAAAASAAAAAAAAAAAJDqKNhO2/XZAvmR1Wynm2lxfIUQwB6TDzqNVOlQgQTgAAAA8AAAAGRVRIVVNEAAAAAAAKAAAAAAAAAAAAAAAEaJlbMAAAAAAAAAABAAAAAAAAAAQAAAAGAAAAAcbgeSm1T8h+V5r+/0u+qUVZIopr5hDeGKj1+u37faSgAAAAEAAAAAEAAAADAAAADwAAAAdIYXNSb2xlAAAAABIAAAAAAAAAAAkOoo2E7b9dkC+ZHVbKebaXF8hRDAHpMPOo1U6VCBBOAAAADwAAAAZPUkFDTEUAAAAAAAEAAAAGAAAAAcbgeSm1T8h+V5r+/0u+qUVZIopr5hDeGKj1+u37faSgAAAAFAAAAAEAAAAHve3Sc2JfmD0Hu+w96oFCzEX1XxFR0uE/NeSf2Vqmia8AAAAH4IWMslKp5yCKjCiGPIRV6yV0LdrLOEfRrCRSwjur0G8AAAABAAAABgAAAAGa0iD0jMkXE/PfK6J0IyPcuITZIHddg6XwO+2iEdE9UAAAABQAAAABADikCAAAAAAAAAJUAAAAAAAATa0AAAABlQgQTgAAAEDSMm2vdKNsB2TCk+Pbb6vSYgq6Zd5F0E4H5BfMi4lwWEcYFAq2Mp+e12wr1qU+Ni7+2BTqZUkb+uK7lVM8PqkN", "c9b2d35ab15c055acb422872a8f1680a84ad6b8ad0d56271cfb74c083edf2007"},
	{"signed stellar V0 envelope (pubnet)", "stellar:pubnet", "AAAAAG5btGuvFysDlQ/whfTBH8NWx1qRgzGpjtSDnJx3krOBAAAAZAAAAAEAAAAAAAAAAAAAAAAAAAABAAAAAAAAAAsAAAAAAAAAZAAAAAAAAAABd5KzgQAAAEAu0xW2vwIqtuAu4/FFLWHBooGpvqn/N6iHgEX45savBk7SyoFGKIlyhG7ETZQ93tbF1OC/5ym6SdXmwIhIPQUD", "5b709eff53cb92c20d2c79e007f6b53ba9be04d6073119d142ffa70d7ea5c7cb"},
	{"signature ends in four zero bytes", "stellar:pubnet", "AAAAAgAAAABuW7RrrxcrA5UP8IX0wR/DVsdakYMxqY7Ug5ycd5KzgQAAAGQAAAAASZYC0wAAAAEAAAAAAAAAAAAAAABw29iAAAAAAAAAAAEAAAAAAAAAAQAAAABuW7RrrxcrA5UP8IX0wR/DVsdakYMxqY7Ug5ycd5KzgQAAAAAAAAAAAJiWgAAAAAAAAAABd5KzgQAAAEDGLpyx0gomLUe6OHNM90dIb/J8FPe2mR/+9m8suCVKNCF3UH6jhJthgRaiYAchg4uS+yAghMuqqTVHvyAAAAAA", "17e5f1f36ff6edc099fc190d24da41e48ab9dd9f0b0be6c6d68d9eba028ed8d3"},
}

const stellarUnknownNetworkXDR = "AAAAAgAAAAAEJr/kOejtZoeowwp8zwNy2Q5PJ4BGTWfjsi8nOzNmrwABOIQDtAXqAA5+nQAAAAEAAAAAAAAAAAAAAABqgtc9AAAAAAAAAAQAAAAAAAAADAAAAAFVU0RDAAAAADuZETgO/piLoKiQDrHP5E82b32+lGvtB3JA9/Yk3xXFAAAAAXlYTE0AAAAAIjbXcP4NPgFSGXXVz3rEhCtwldaxqddo0+mmMumZBr4AAAAAWC0vBxJeEy11BinxAAAAAG57ynoAAAAAAAAADAAAAAFVU0RDAAAAADuZETgO/piLoKiQDrHP5E82b32+lGvtB3JA9/Yk3xXFAAAAAXlYTE0AAAAAIjbXcP4NPgFSGXXVz3rEhCtwldaxqddo0+mmMumZBr4AAAACy0F4AAGqIxIKms6JAAAAAG57nJcAAAAAAAAAAwAAAAF5WExNAAAAACI213D+DT4BUhl11c96xIQrcJXWsanXaNPppjLpmQa+AAAAAVVTREMAAAAAO5kROA7+mIugqJAOsc/kTzZvfb6Ua+0HckD39iTfFcUAAAAAstBeAA36DJRYpIptAAAAAG52hUIAAAAAAAAAAwAAAAF5WExNAAAAACI213D+DT4BUhl11c96xIQrcJXWsanXaNPppjLpmQa+AAAAAVVTREMAAAAAO5kROA7+mIugqJAOsc/kTzZvfb6Ua+0HckD39iTfFcUAAAACy0F4AA0hty1TaHPUAAAAAG56kgAAAAAAAAAAATszZq8AAABAxLKQz2a6NY74WUVxGbWvafiKXhExlAIu2WM2MKUNOidwRDZb3EDY/JP2TbM5rWXlmwZGKHE8iLYs19u/n2LgAA=="

func raw(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestSolanaTransactionID(t *testing.T) {
	if id, err := solanaTransactionID(solanaTx); err != nil || id != solanaTxID {
		t.Fatalf("got %q, %v", id, err)
	}
	if _, err := solanaTransactionID(solanaBadTx); err == nil {
		t.Fatal("an unsigned transaction should fail")
	}
}

func TestSuiDigest(t *testing.T) {
	if d, err := suiDigest(suiTx); err != nil || d != suiTxDigest {
		t.Fatalf("got %q, %v", d, err)
	}
}

// The bytes arrive as a JSON array, as an object with numeric keys, or as a
// Buffer-like object wrapping either.
func TestNearTransactionID(t *testing.T) {
	keyed := map[string]int{}
	for i, b := range nearTxBytes {
		keyed[fmt.Sprint(i)] = b
	}
	for name, v := range map[string]any{
		"array": nearTxBytes, "numeric keys": keyed,
		"buffer object":          map[string]any{"type": "Buffer", "data": nearTxBytes},
		"buffer with keyed data": map[string]any{"type": "buffer", "data": keyed},
	} {
		if id, err := nearTransactionID(raw(v)); err != nil || id != nearTxID {
			t.Errorf("%s: got %q, %v", name, id, err)
		}
	}
	if _, err := nearTransactionID(raw("not bytes")); err == nil {
		t.Error("a string is not a byte encoding")
	}
}

func TestAlgorandTransactionID(t *testing.T) {
	if id, err := algorandTransactionID(algorandTx); err != nil || id != algorandTxID {
		t.Fatalf("got %q, %v", id, err)
	}
	if _, err := algorandTransactionID("AAAA"); err == nil {
		t.Fatal("a non-map should fail")
	}
}

func TestMsgpackSkip(t *testing.T) {
	// {"a": [1, "xy", nil, true, -1, 300, 70000, 1.5], "b": bin(3), "c": {"d": 2}}
	doc := []byte{0x83,
		0xa1, 'a', 0x98, 0x01, 0xa2, 'x', 'y', 0xc0, 0xc3, 0xff, 0xcd, 0x01, 0x2c, 0xce, 0x00, 0x01, 0x11, 0x70, 0xcb, 0x3f, 0xf8, 0, 0, 0, 0, 0, 0,
		0xa1, 'b', 0xc4, 0x03, 1, 2, 3,
		0xa1, 'c', 0x81, 0xa1, 'd', 0x02}
	if n, err := msgpackSkip(doc); err != nil || n != len(doc) {
		t.Fatalf("skip = %d, %v; want %d", n, err, len(doc))
	}
	if v, err := msgpackMapValue(doc, "c"); err != nil || string(v) != string([]byte{0x81, 0xa1, 'd', 0x02}) {
		t.Fatalf("value c = %x, %v", v, err)
	}
	if _, err := msgpackMapValue(doc, "z"); err == nil {
		t.Fatal("missing key should fail")
	}
	if _, err := msgpackSkip(doc[:10]); err == nil {
		t.Fatal("a truncated document should fail")
	}
}

func TestCosmosSignDirectHash(t *testing.T) {
	if h, err := cosmosSignDirectHash(json.RawMessage(cosmosResult)); err != nil || h != cosmosTxHash {
		t.Fatalf("got %q, %v", h, err)
	}
}

func TestStellarTransactionHash(t *testing.T) {
	for _, v := range stellarVectors {
		if h, err := stellarTransactionHash(v.xdr, v.chain); err != nil || h != v.hash {
			t.Errorf("%s: got %q, %v", v.name, h, err)
		}
	}
	if h, err := stellarTransactionHash(stellarVectors[0].xdr, ""); err != nil || h != stellarVectors[0].hash {
		t.Errorf("pubnet should be the default network: %q, %v", h, err)
	}
	if _, err := stellarTransactionHash(stellarUnknownNetworkXDR, "stellar:futurenet"); err == nil {
		t.Error("an unknown network should fail")
	}
	if _, err := stellarTransactionHash("AAAA", ""); err == nil {
		t.Error("a short envelope should fail")
	}
	if _, err := stellarTransactionHash(stellarBadXDR, "stellar:pubnet"); err == nil {
		t.Error("an unknown envelope type should fail")
	}
}

func TestPolkadotExtrinsicHash(t *testing.T) {
	hashes := txHashes("polkadot_signTransaction", json.RawMessage(polkadotParams), json.RawMessage(polkadotResult))
	if len(hashes) != 1 || hashes[0] != polkadotHash {
		t.Fatalf("got %v", hashes)
	}
}

func TestCantonTransactionHashes(t *testing.T) {
	cmd := func(choice string, amount, instrument any) map[string]any {
		return map[string]any{"ExerciseCommand": map[string]any{"templateId": "<id>", "contractId": "<cid>", "choice": choice,
			"choiceArgument": map[string]any{"expectedAdmin": "<admin>", "transfer": map[string]any{"sender": "s", "receiver": "r",
				"amount": amount, "instrumentId": map[string]any{"admin": "<admin>", "id": instrument}}}}}
	}
	result := raw(map[string]any{"payload": map[string]any{"updateId": "0x123"}})
	cases := []struct {
		name   string
		params any
		want   []string
	}{
		{"one transfer", map[string]any{"commands": []any{cmd("TransferFactory_Transfer", "100", "Amulet")}}, []string{"0x123:100:Amulet"}},
		{"two commands", map[string]any{"commands": []any{cmd("TransferFactory_Transfer", "100", "Amulet"), cmd("DelegateProxy_TransferFactory_Transfer", "250", "CC")}}, []string{"0x123:100:Amulet", "0x123:250:CC"}},
		{"numbers coerced", map[string]any{"commands": []any{cmd("TransferFactory_Transfer", 1000, 7)}}, []string{"0x123:1000:7"}},
		{"unsupported choice falls back to the update id", map[string]any{"commands": []any{cmd("TransferInstruction_Accept", "1", "CC")}}, []string{"0x123"}},
		{"no commands", map[string]any{}, []string{"0x123"}},
	}
	for _, c := range cases {
		if got := txHashes("canton_prepareSignExecute", raw(c.params), result); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if got := txHashes("canton_prepareSignExecute", raw(cases[0].params), raw(map[string]any{"payload": map[string]any{}})); fmt.Sprint(got) != `[{"payload":{}}]` {
		t.Errorf("no update id should fall back to the result text, got %v", got)
	}
}

func TestTxHashesByMethod(t *testing.T) {
	cases := []struct {
		method string
		params any
		result any
		want   []string
	}{
		{"eth_sendTransaction", nil, "0xabc", []string{"0xabc"}},
		{"eth_sendRawTransaction", nil, "0xdef", []string{"0xdef"}},
		{"wallet_sendCalls", nil, "0xbundle", []string{"0xbundle"}},
		{"wallet_sendCalls", nil, map[string]any{"id": "0xbundle", "capabilities": map[string]any{"caip345": map[string]any{"transactionHashes": []string{"0x1", "0x2"}}}}, []string{"0xbundle", "0x1", "0x2"}},
		{"wallet_sendCalls", nil, map[string]any{"id": "0xbundle"}, []string{"0xbundle"}},
		{"solana_signTransaction", nil, map[string]any{"signature": "sig1"}, []string{"sig1"}},
		{"solana_signAndSendTransaction", nil, map[string]any{"signature": "sig2"}, []string{"sig2"}},
		{"solana_signAllTransactions", nil, map[string]any{"transactions": []string{solanaTx}}, []string{solanaTxID}},
		{"sui_signAndExecuteTransaction", nil, map[string]any{"digest": "d1"}, []string{"d1"}},
		{"sui_signTransaction", nil, map[string]any{"transactionBytes": suiTx, "signature": "x"}, []string{suiTxDigest}},
		{"hedera_signAndExecuteTransaction", nil, map[string]any{"transactionId": "0.0.1@1"}, []string{"0.0.1@1"}},
		{"hedera_executeTransaction", nil, map[string]any{"transactionId": "0.0.1@2"}, []string{"0.0.1@2"}},
		{"near_signTransaction", nil, nearTxBytes, []string{nearTxID}},
		{"near_signTransactions", nil, []any{nearTxBytes, nearTxBytes}, []string{nearTxID, nearTxID}},
		{"tron_signTransaction", nil, map[string]any{"txID": "t1", "raw_data": "x"}, []string{"t1"}},
		{"xrpl_signTransaction", nil, map[string]any{"tx_json": map[string]any{"hash": "h1"}}, []string{"h1"}},
		{"xrpl_signTransactionFor", nil, map[string]any{"tx_json": map[string]any{"hash": "h2"}}, []string{"h2"}},
		{"algo_signTxn", nil, []any{algorandTx, nil}, []string{algorandTxID}},
		{"algo_signTxn", nil, algorandTx, []string{algorandTxID}},
		{"sendTransfer", nil, map[string]any{"txid": "btc1"}, []string{"btc1"}},
		{"stacks_stxTransfer", nil, map[string]any{"txId": "stx1"}, []string{"stx1"}},
		{"cosmos_signDirect", nil, json.RawMessage(cosmosResult), []string{cosmosTxHash}},
		{"stellar_signXDR", map[string]any{"chain": "stellar:testnet", "xdr": "x"}, map[string]any{"signedXDR": stellarVectors[2].xdr}, []string{stellarVectors[2].hash}},
		{"stellar_signAndSubmitXDR", nil, map[string]any{"tx_hash": "st1"}, []string{"st1"}},
		// Methods the table does not cover report the result itself: a string
		// as it is, anything else as its JSON text.
		{"personal_sign", nil, "0xsignature", []string{"0xsignature"}},
		{"eth_signTypedData_v4", nil, "0xsignature", []string{"0xsignature"}},
		{"xyz_sendTransaction", nil, map[string]any{"hash": "0xabc", "block": 7}, []string{`{"block":7,"hash":"0xabc"}`}},
		{"xyz_sign", nil, 42, []string{"42"}},
		{"xyz_sign", nil, []any{"0x1", "0x2"}, []string{`["0x1","0x2"]`}},
		// A covered method whose result cannot be read falls back to the same.
		{"solana_signTransaction", nil, map[string]any{"other": 1}, []string{`{"other":1}`}},
		{"solana_signTransaction", nil, "a string where an object was expected", []string{"a string where an object was expected"}},
		{"sui_signTransaction", nil, map[string]any{"transactionBytes": "!!!"}, []string{`{"transactionBytes":"!!!"}`}},
		{"near_signTransaction", nil, "x", []string{"x"}},
		{"tron_signTransaction", nil, map[string]any{"txID": 5}, []string{`{"txID":5}`}},
		// Null and empty results report nothing.
		{"eth_sendTransaction", nil, nil, nil},
		{"personal_sign", nil, nil, nil},
		{"personal_sign", nil, "", nil},
	}
	for _, c := range cases {
		var result json.RawMessage
		if c.result != nil {
			result = raw(c.result)
		}
		if got := txHashes(c.method, raw(c.params), result); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s with result %s: got %v, want %v", c.method, strings.TrimSpace(string(result)), got, c.want)
		}
	}
}

func TestContractAddresses(t *testing.T) {
	cases := []struct {
		name   string
		params any
		want   []string
	}{
		{"calldata", []any{map[string]any{"to": "0xc0ffee", "data": "0xa9059cbb0001"}}, []string{"0xc0ffee"}},
		{"bare 0x counts like the JS client", []any{map[string]any{"to": "0xc0ffee", "data": "0x", "value": "0x1"}}, []string{"0xc0ffee"}},
		{"object form", map[string]any{"to": "0xc0ffee", "data": "0x00"}, []string{"0xc0ffee"}},
		{"odd length", []any{map[string]any{"to": "0xc0ffee", "data": "0xabc"}}, nil},
		{"not hex", []any{map[string]any{"to": "0xc0ffee", "data": "0xzz"}}, nil},
		{"no prefix", []any{map[string]any{"to": "0xc0ffee", "data": "a9"}}, nil},
		{"no data", []any{map[string]any{"to": "0xc0ffee"}}, nil},
		{"no to", []any{map[string]any{"data": "0x00"}}, nil},
		{"personal_sign params", []any{"0x68656c6c6f", "0xc0ffee"}, nil},
		{"empty", []any{}, nil},
		{"null", nil, nil},
	}
	for _, c := range cases {
		if got := contractAddresses(raw(c.params)); fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTNVAddTo(t *testing.T) {
	p := map[string]any{"topic": "t"}
	(*tnv)(nil).addTo(p)
	(&tnv{}).addTo(p)
	if len(p) != 1 {
		t.Fatalf("empty data added fields: %v", p)
	}
	full := &tnv{correlationID: 7, rpcMethods: []string{"m"}, chainID: "eip155:1", txHashes: []string{"h"}, contractAddresses: []string{"c"},
		approvedChains: []string{"eip155:1"}, approvedMethods: []string{"personal_sign"}, approvedEvents: []string{"chainChanged"},
		sessionProperties: map[string]string{"k": "v"}, scopedProperties: map[string]json.RawMessage{"eip155": json.RawMessage(`{}`)}}
	full.addTo(p)
	for _, key := range []string{"correlationId", "rpcMethods", "chainId", "txHashes", "contractAddresses", "approvedChains", "approvedMethods", "approvedEvents", "sessionProperties", "scopedProperties"} {
		if _, ok := p[key]; !ok {
			t.Errorf("missing %s", key)
		}
	}
	if p["correlationId"] != int64(7) {
		t.Errorf("correlationId = %v (%T)", p["correlationId"], p["correlationId"])
	}
}
