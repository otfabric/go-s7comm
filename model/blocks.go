// SPDX-License-Identifier: MIT

package model

// BlockType represents an S7 block type
type BlockType uint8

const (
	BlockOB  BlockType = 0x38
	BlockDB  BlockType = 0x41
	BlockSDB BlockType = 0x42
	BlockFC  BlockType = 0x43
	BlockSFC BlockType = 0x44
	BlockFB  BlockType = 0x45
	BlockSFB BlockType = 0x46
)

func (b BlockType) String() string {
	switch b {
	case BlockOB:
		return "OB"
	case BlockDB:
		return "DB"
	case BlockSDB:
		return "SDB"
	case BlockFC:
		return "FC"
	case BlockSFC:
		return "SFC"
	case BlockFB:
		return "FB"
	case BlockSFB:
		return "SFB"
	default:
		return "?"
	}
}

// BlockLang represents the programming language of a block
type BlockLang uint8

const (
	BlockLangAWL   BlockLang = 0x01
	BlockLangKOP   BlockLang = 0x02
	BlockLangFUP   BlockLang = 0x03
	BlockLangSCL   BlockLang = 0x04
	BlockLangDB    BlockLang = 0x05
	BlockLangGraph BlockLang = 0x06
)

func (l BlockLang) String() string {
	switch l {
	case BlockLangAWL:
		return "AWL"
	case BlockLangKOP:
		return "KOP"
	case BlockLangFUP:
		return "FUP"
	case BlockLangSCL:
		return "SCL"
	case BlockLangDB:
		return "DB"
	case BlockLangGraph:
		return "GRAPH"
	default:
		return "?"
	}
}

// BlockInfo contains information about a program block
type BlockInfo struct {
	Type       BlockType
	Number     int
	Language   BlockLang
	Flags      byte
	LoadMemory int
	LocalData  int
	MC7Size    int
	SBBLength  int
	Author     string
	Family     string
	Name       string
	Version    string
	CheckSum   uint16
}

// BlockData contains the raw block content
type BlockData struct {
	Info BlockInfo
	Data []byte
}
