// Command icon-gen renders the shared SVG into a desktop PNG and a multi-size Windows ICO.
// Run from the repository root with: go run -buildvcs=false ./tools/icon-gen
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"golang.org/x/image/draw"
)

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	const dir = "internal/branding/"
	source, err := os.ReadFile(dir + "server.svg")
	if err != nil {
		return err
	}
	render := func(size int) ([]byte, error) {
		icon, err := oksvg.ReadIconStream(bytes.NewReader(source), oksvg.StrictErrorMode)
		if err != nil {
			return nil, err
		}
		const scale = 4
		canvas := image.NewRGBA(image.Rect(0, 0, size*scale, size*scale))
		icon.SetTarget(0, 0, float64(size*scale), float64(size*scale))
		scanner := rasterx.NewScannerGV(size*scale, size*scale, canvas, canvas.Bounds())
		icon.Draw(rasterx.NewDasher(size*scale, size*scale, scanner), 1)
		result := image.NewRGBA(image.Rect(0, 0, size, size))
		draw.CatmullRom.Scale(result, result.Bounds(), canvas, canvas.Bounds(), draw.Src, nil)
		var encoded bytes.Buffer
		if err = png.Encode(&encoded, result); err != nil {
			return nil, err
		}
		return encoded.Bytes(), nil
	}
	large, err := render(256)
	if err != nil {
		return err
	}
	if err = os.WriteFile(dir+"server.png", large, 0644); err != nil {
		return err
	}
	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	images := make([][]byte, len(sizes))
	for i, size := range sizes {
		images[i], err = render(size)
		if err != nil {
			return err
		}
	}
	// ICO entries contain PNGs; width/height zero represent a 256-pixel image.
	data := make([]byte, 6+16*len(sizes))
	binary.LittleEndian.PutUint16(data[2:4], 1)
	binary.LittleEndian.PutUint16(data[4:6], uint16(len(sizes)))
	offset := len(data)
	for i, size := range sizes {
		entry := data[6+16*i : 6+16*(i+1)]
		if size < 256 {
			entry[0] = byte(size)
			entry[1] = byte(size)
		}
		binary.LittleEndian.PutUint16(entry[4:6], 1)
		binary.LittleEndian.PutUint16(entry[6:8], 32)
		binary.LittleEndian.PutUint32(entry[8:12], uint32(len(images[i])))
		binary.LittleEndian.PutUint32(entry[12:16], uint32(offset))
		offset += len(images[i])
	}
	for _, encoded := range images {
		data = append(data, encoded...)
	}
	return os.WriteFile(dir+"server.ico", data, 0644)
}
