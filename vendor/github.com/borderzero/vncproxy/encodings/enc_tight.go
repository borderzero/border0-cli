package encodings

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/borderzero/vncproxy/common"
)

var TightMinToCompress int = 12

const (
	TightExplicitFilter = 0x04
	TightFill           = 0x08
	TightJpeg           = 0x09
	TightPNG            = 0x10

	TightFilterCopy     = 0x00
	TightFilterPalette  = 0x01
	TightFilterGradient = 0x02
)

type TightEncoding struct {
	bytes []byte
}

func (*TightEncoding) Type() int32 { return int32(common.EncTight) }

func calcTightBytePerPixel(pf *common.PixelFormat) int {
	bytesPerPixel := int(pf.BPP / 8)

	var bytesPerPixelTight int
	if 24 == pf.Depth && 32 == pf.BPP {
		bytesPerPixelTight = 3
	} else {
		bytesPerPixelTight = bytesPerPixel
	}
	return bytesPerPixelTight
}

func (z *TightEncoding) WriteTo(w io.Writer) (n int, err error) {
	return w.Write(z.bytes)
}

func StoreBytes(bytes *bytes.Buffer, data []byte) error {
	if _, err := bytes.Write(data); err != nil {
		return fmt.Errorf("error saving bytes: %v", err)
	}
	return nil
}

func (t *TightEncoding) Read(pixelFmt *common.PixelFormat, rect *common.Rectangle, r *common.RfbReadHelper) (common.IEncoding, error) {
	bytesPixel := calcTightBytePerPixel(pixelFmt)

	r.StartByteCollection()
	defer func() {
		t.bytes = r.EndByteCollection()
	}()

	compctl, err := r.ReadUint8()

	if err != nil {
		return nil, fmt.Errorf("error in handling tight encoding: %v", err)
	}

	//move it to position (remove zlib flush commands)
	compType := compctl >> 4 & 0x0F

	switch compType {
	case TightFill:
		_, err := r.ReadBytes(int(bytesPixel))
		if err != nil {
			return nil, fmt.Errorf("error in handling tight encoding: %v", err)
		}

		return t, nil
	case TightJpeg:
		if pixelFmt.BPP == 8 {
			return nil, errors.New("Tight encoding: JPEG is not supported in 8 bpp mode")
		}

		len, err := r.ReadCompactLen()

		if err != nil {
			return nil, err
		}
		_, err = r.ReadBytes(len)
		if err != nil {
			return nil, err
		}

		return t, nil
	default:

		if compType > TightJpeg {
			fmt.Println("Compression control byte is incorrect!")
		}

		if err := handleTightFilters(compctl, pixelFmt, rect, r); err != nil {
			return nil, err
		}

		return t, nil
	}
}

func handleTightFilters(subencoding uint8, pixelFmt *common.PixelFormat, rect *common.Rectangle, r *common.RfbReadHelper) error {

	var FILTER_ID_MASK uint8 = 0x40

	var filterid uint8
	var err error

	if (subencoding & FILTER_ID_MASK) > 0 { // filter byte presence
		filterid, err = r.ReadUint8()

		if err != nil {
			return fmt.Errorf("error in handling tight encoding, reading filterid: %v", err)
		}
	}

	bytesPixel := calcTightBytePerPixel(pixelFmt)

	lengthCurrentbpp := int(bytesPixel) * int(rect.Width) * int(rect.Height)

	switch filterid {
	case TightFilterPalette: //PALETTE_FILTER

		colorCount, err := r.ReadUint8()
		if err != nil {
			return fmt.Errorf("error in handling tight encoding, reading TightFilterPalette: %v", err)
		}

		paletteSize := int(colorCount) + 1 // add one more

		//complete palette
		_, err = r.ReadBytes(int(paletteSize) * bytesPixel)
		if err != nil {
			return fmt.Errorf("error in handling tight encoding, reading TightFilterPalette.paletteSize: %v", err)
		}

		var dataLength int
		if paletteSize == 2 {
			dataLength = int(rect.Height) * ((int(rect.Width) + 7) / 8)
		} else {
			dataLength = int(rect.Width) * int(rect.Height)
		}
		_, err = r.ReadTightData(dataLength)
		if err != nil {
			return fmt.Errorf("error in handling tight encoding, Reading Palette: %v", err)
		}

	case TightFilterGradient: //GRADIENT_FILTER
		_, err := r.ReadTightData(lengthCurrentbpp)
		if err != nil {
			return fmt.Errorf("error in handling tight encoding, Reading GRADIENT_FILTER: %v", err)
		}

	case TightFilterCopy: //BASIC_FILTER
		_, err := r.ReadTightData(lengthCurrentbpp)
		if err != nil {
			return fmt.Errorf("error in handling tight encoding, Reading BASIC_FILTER: %v", err)
		}

	default:
		return fmt.Errorf("bad tight filter id: %d", filterid)
	}

	return nil
}
