package frame

import "bytes"
import "encoding/binary"
import "fmt"
import dbg "fmt"

const (
	ErrIsNotNil        = "the reserved bits are not all 0"
	ErrInvalidSyncCode = "sync code is invalid (must be 11111111111110 or 16382 decimal): %d"
	ErrInvalidEncoding = "UTF8 invalid encoding"
)

type Frame struct {
	Header    FrameHeader
	SubFrames []SubFrame
	Footer    FrameFooter
}

type FrameHeader struct {
	HasVariableBlockSize         bool
	BlockSizeInterChannelSamples uint8
	SampleRate                   uint8
	ChannelAssignment            uint8
	SampleSize                   uint8
	CRC                          uint8
}

type SubFrame struct {
	Header SubFrameHeader
	Block  interface{}
}

type SubFrameHeader struct {
	frameType  uint8
	wastedBits []byte
}

type SubFrameConstant struct {
	Value []byte
}

type SubFrameFixed struct {
	WarmUpSamples []byte
	Residual      []Residual
}

type SubFrameLpc struct {
	WarmUpSamples         []byte
	Precision             uint8
	ShiftNeeded           uint8
	PredictorCoefficients []byte
}

type SubFrameVerbatim struct {
	UnencodedSubblock []byte
}

type Residual struct {
	UsesRice2 bool
}

type Rice struct {
	PartitionOrder uint8
	Partitions     []RicePartition
}

type Rice2 struct {
	PartitionOrder uint8
	Partitions     []Rice2Partition
}

type RicePartition struct {
	EncodingParameter uint16
}

type Rice2Partition struct{}

type FrameFooter struct{}

func Decode(block []byte) (f *Frame, err error) {
	const (
		SyncCodeMask         = 0xFFFC
		FirstReservedMask    = 0x0002
		BlockingStrategyMask = 0x0001

		BlockSizeInterChannelSamplesMask = 0xF0
		SampleRateMask                   = 0x0F

		ChannelAssignmentMask = 0xF0
		SampleSizeMask        = 0x0E
		SecondReservedMask    = 0x01
	)

	f = new(Frame)

	buf := bytes.NewBuffer(block)

	//Sync Code, Reserved and blocking strategy (size: 2 bytes)
	bits := binary.BigEndian.Uint16(buf.Next(2))
	if (SyncCodeMask&bits)>>2 != 16382 {
		return nil, fmt.Errorf(ErrInvalidSyncCode, (SyncCodeMask&bits)>>2)
	}

	//Reserved values most be 0
	if (FirstReservedMask&bits)>>1 != 0 {
		return nil, fmt.Errorf(ErrIsNotNil)
	}

	//If BlockingStrategyMask is 0 it has fixed blocksize instead of variable
	if BlockingStrategyMask&bits != 0 {
		f.Header.HasVariableBlockSize = true
	}

	// Block size in inter channel samples and Sample rate (size: 1 byte)
	aByte := buf.Next(1)[0]

	f.Header.BlockSizeInterChannelSamples = uint8((BlockSizeInterChannelSamplesMask & aByte) >> 4)
	f.Header.SampleRate = uint8(SampleRateMask & aByte)

	//Channel Assignment, Sample Size in bits and reserved bit (size: 1 byte)
	aByte = buf.Next(1)[0]

	f.Header.ChannelAssignment = uint8(ChannelAssignmentMask & aByte >> 4)
	f.Header.SampleSize = uint8(SampleSizeMask & aByte >> 1)

	if (SecondReservedMask & bits) != 0 {
		return nil, fmt.Errorf(ErrIsNotNil)
	}

	///I have no idea how to decrypt this part of the spec:
	///EDIT: holy fuck I found a clue http://comments.gmane.org/gmane.comp.audio.compression.flac.devel/3031
	// <?> 	if(variable blocksize)
	//   		<8-56>:"UTF-8" coded sample number (decoded number is 36 bits)
	// 		else
	//   		<8-48>:"UTF-8" coded frame number (decoded number is 31 bits) 
	if f.Header.HasVariableBlockSize {

	} else {
		// var frameNum uint32
		dbg.Println("Fixed!")
		readValue, err := getUTF8Num(buf.Next(1))
		if err != nil {
			return nil, err
		}
		dbg.Println(readValue)
	}

	// - If blocksize index = 6, read 8 bits from the stream. The true block 
	// size is the read value + 1.
	// - If blocksize index = 7, read 16 bits from the stream. The true block 
	// size is the read value + 1.
	///I have no idea how to decrypt this part of the spec:
	// <?> 	if(blocksize bits == 011x)
	//    		8/16 bit (blocksize-1)
	if f.Header.BlockSizeInterChannelSamples == 6 {
		dbg.Println("True block size is:", uint8(buf.Next(1)[0])+1)
	} else if f.Header.BlockSizeInterChannelSamples == 7 {
		dbg.Println("True block size is:", binary.BigEndian.Uint16(buf.Next(2))+1)
	}

	// - If sample index is 12, read 8 bits from the stream. The true sample 
	// rate is the read value * 1000.
	// - If sample index is 13, read 16 bits from the stream. The true sample 
	// rate is the read value.
	// - If sample index is 14, read 16 bits from the stream. The true sample 
	// rate is the read value * 10.
	// <?> 	if(sample rate bits == 11xx)
	//    		8/16 bit sample rate 
	switch f.Header.SampleRate {
	case 12:
		dbg.Println("True sample rate is:", uint64(buf.Next(1)[0])*1000)
	case 13:
		dbg.Println("True sample rate is:", binary.BigEndian.Uint16(buf.Next(2)))
	case 14:
		dbg.Println("True sample rate is:", binary.BigEndian.Uint16(buf.Next(2))*10)
	}

	///Add this
	// The "blocking strategy" bit must be the same throughout the entire stream.
	// The "blocking strategy" bit determines how to calculate the sample number of the first sample in the frame. If the bit is 0 (fixed-blocksize), the frame header encodes the frame number as above, and the frame's starting sample number will be the frame number times the blocksize. If it is 1 (variable-blocksize), the frame header encodes the frame's starting sample number itself. (In the case of a fixed-blocksize stream, only the last block may be shorter than the stream blocksize; its starting sample number will be calculated as the frame number times the previous frame's blocksize, or zero if it is the first frame).
	// The "UTF-8" coding used for the sample/frame number is the same variable length code used to store compressed UCS-2, extended to handle larger input.

	f.Header.CRC = uint8(buf.Next(1)[0])

	return f, nil
}

func getUTF8Num(block []byte) (readValue uint64, err error) {
	//Well, as the documentation states, it uses the same method as in UTF-8 to store variable length integers
	const (
		endMask        = 0x80
		invalidMask    = 0xC0
		twoLeadingMask = 0xC0
		leadMask       = 0x80
	)

	buf := bytes.NewBuffer(block)

	// var sampleNum uint32

	b0 := buf.Next(1)[0] //read one byte B0 from the stream
	switch {
	case b0&endMask == 0:
		///if B0 = 0xxxxxxx then the read value is B0 -> end
		return uint64(b0), nil
	case b0&invalidMask != 128:
		return 0, fmt.Errorf(ErrInvalidEncoding)
		///- if B0 = 10xxxxxx, the encoding is invalid
	case b0&twoLeadingMask == 192:
		var leadOnes uint8
		for (b0 & leadMask) > 0 {
			leadOnes++
			b0 <<= 1
		}
		fmt.Println(b0, ":", leadOnes)
		///determine numLeadingBinary1sMinus1
		// - if B0 = 11xxxxxx, set L to the number of leading binary 1s minus 1:
		//      B0 = 110xxxxx -> L = 1
		//      B0 = 1110xxxx -> L = 2
		//      B0 = 11110xxx -> L = 3
		//      B0 = 111110xx -> L = 4
		//      B0 = 1111110x -> L = 5
		//      B0 = 11111110 -> L = 6
	}

	/*


		- assign the bits following the encoding (the x bits in the examples) to 
		a variable R with a magnitude of at least 56 bits
		- loop from 1 to L
		     - left shift R 6 bits
		     - read B from the stream
		     - if B does not match 10xxxxxx, the encoding is invalid
		     - set R = R or <the lower 6 bits from B>
		- the read value is R

		The following two fields depend on the block size and sample rate index 
		read earlier in the header:

		- If blocksize index = 6, read 8 bits from the stream. The true block 
		size is the read value + 1.
		- If blocksize index = 7, read 16 bits from the stream. The true block 
		size is the read value + 1.

		- If sample index is 12, read 8 bits from the stream. The true sample 
		rate is the read value * 1000.
		- If sample index is 13, read 16 bits from the stream. The true sample 
		rate is the read value.
		- If sample index is 14, read 16 bits from the stream. The true sample 
		rate is the read value * 10.
	*/
	return 0, nil
}