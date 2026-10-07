package storage

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/HugoSmits86/nativewebp"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

// maxPixels bounds decoding so a small file cannot claim a gigantic canvas.
const maxPixels = 100_000_000

// imageProcess is the pure-Go replacement for the libvips helper the Rails
// reference relies on. With an empty output it reports the display size of the
// image (EXIF orientation applied). Otherwise it auto-rotates the image, shrinks it
// to fit width x height (never enlarging; zero means unconstrained), sharpens the
// result like vips' thumbnail pipeline does, and writes it in the format implied by
// the output file's extension.
func imageProcess(path, output string, width, height int) (int, int, error) {
	if strings.ContainsRune(path, 0) || strings.ContainsRune(output, 0) {
		return 0, 0, errors.New("invalid image path")
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	cfg, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxPixels {
		return 0, 0, errors.New("image dimensions out of range")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return 0, 0, err
	}
	orientation := exifOrientation(file)
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return 0, 0, err
	}
	if output == "" {
		if orientation >= 5 {
			return cfg.Height, cfg.Width, nil
		}
		return cfg.Width, cfg.Height, nil
	}
	src, _, err := image.Decode(file)
	if err != nil {
		return 0, 0, err
	}
	src = orient(src, orientation)
	if width > 0 || height > 0 {
		src = sharpen(shrink(src, width, height))
	}
	if err = encodeImage(output, src); err != nil {
		return 0, 0, err
	}
	b := src.Bounds()
	return b.Dx(), b.Dy(), nil
}

// shrink scales src down to fit the box, preserving the aspect ratio.
func shrink(src image.Image, width, height int) image.Image {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	scale := 1.0
	if width > 0 && w > width {
		scale = float64(width) / float64(w)
	}
	if height > 0 && h > height {
		if s := float64(height) / float64(h); s < scale {
			scale = s
		}
	}
	if scale >= 1 {
		return src
	}
	nw, nh := max(1, int(float64(w)*scale+0.5)), max(1, int(float64(h)*scale+0.5))
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	return dst
}

// sharpen applies the 3x3 kernel (-1 ... 32 ... -1) / 24 vips uses after a thumbnail.
func sharpen(src image.Image) image.Image {
	b := src.Bounds()
	in := image.NewNRGBA(b)
	draw.Draw(in, b, src, b.Min, draw.Src)
	out := image.NewNRGBA(b)
	w, h := b.Dx(), b.Dy()
	at := func(x, y int) []uint8 {
		x, y = min(max(x, 0), w-1), min(max(y, 0), h-1)
		i := in.PixOffset(b.Min.X+x, b.Min.Y+y)
		return in.Pix[i : i+4]
	}
	for y := range h {
		for x := range w {
			o := out.PixOffset(b.Min.X+x, b.Min.Y+y)
			centre := at(x, y)
			var sum [3]int
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					weight := -1
					if dx == 0 && dy == 0 {
						weight = 32
					}
					p := at(x+dx, y+dy)
					for c := range 3 {
						sum[c] += weight * int(p[c])
					}
				}
			}
			for c := range 3 {
				out.Pix[o+c] = uint8(min(max((sum[c]+12)/24, 0), 255))
			}
			out.Pix[o+3] = centre[3]
		}
	}
	return out
}

func encodeImage(output string, img image.Image) error {
	var buf bytes.Buffer
	var err error
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(output), ".")) {
	case "png":
		err = png.Encode(&buf, img)
	case "jpg", "jpeg":
		err = jpeg.Encode(&buf, flatten(img), &jpeg.Options{Quality: 85})
	case "gif":
		err = gif.Encode(&buf, img, nil)
	case "tiff":
		err = tiff.Encode(&buf, img, nil)
	case "webp":
		err = nativewebp.Encode(&buf, img, nil)
	default:
		err = fmt.Errorf("unsupported output format %q", filepath.Ext(output))
	}
	if err != nil {
		return err
	}
	return os.WriteFile(output, buf.Bytes(), 0o644)
}

// flatten composites transparency onto white, since JPEG has no alpha channel.
func flatten(img image.Image) image.Image {
	b := img.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, b, img, b.Min, draw.Over)
	return dst
}

// orient applies an EXIF orientation (1-8) so the pixels are upright.
func orient(src image.Image, orientation int) image.Image {
	if orientation < 2 || orientation > 8 {
		return src
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	nw, nh := w, h
	if orientation >= 5 {
		nw, nh = h, w
	}
	dst := image.NewNRGBA(image.Rect(0, 0, nw, nh))
	for y := range h {
		for x := range w {
			var dx, dy int
			switch orientation {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			}
			dst.Set(dx, dy, src.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}

// exifOrientation reads the Orientation tag of a JPEG's APP1/Exif segment, or
// returns 1 when there is none (or the file is not a JPEG).
func exifOrientation(r io.Reader) int {
	var soi [2]byte
	if _, err := io.ReadFull(r, soi[:]); err != nil || soi != [2]byte{0xff, 0xd8} {
		return 1
	}
	for {
		var header [4]byte
		if _, err := io.ReadFull(r, header[:]); err != nil || header[0] != 0xff {
			return 1
		}
		marker, length := header[1], int(binary.BigEndian.Uint16(header[2:]))-2
		if marker == 0xda || length < 0 { // start of scan: no more metadata
			return 1
		}
		if marker != 0xe1 {
			if _, err := io.CopyN(io.Discard, r, int64(length)); err != nil {
				return 1
			}
			continue
		}
		segment := make([]byte, length)
		if _, err := io.ReadFull(r, segment); err != nil || len(segment) < 14 || string(segment[:6]) != "Exif\x00\x00" {
			return 1
		}
		return tiffOrientation(segment[6:])
	}
}

func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	offset := int(order.Uint32(t[4:]))
	if offset < 8 || offset+2 > len(t) {
		return 1
	}
	count := int(order.Uint16(t[offset:]))
	for i := range count {
		entry := offset + 2 + i*12
		if entry+12 > len(t) {
			break
		}
		if order.Uint16(t[entry:]) == 0x0112 {
			if v := int(order.Uint16(t[entry+8:])); v >= 1 && v <= 8 {
				return v
			}
			break
		}
	}
	return 1
}
