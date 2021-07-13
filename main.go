package main

import (
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"log"
	"math"
	"net/http"
	"os"
	"regexp"
	"strconv"
)

func main() {
	listenAddr := flag.String("listen-addr", ":8080", "address to listen for tile requests on")
	flag.Parse()
	log.Fatal(http.ListenAndServe(*listenAddr, tileServer()))
}

func tileServer() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		coords, err := extractTileCoords(r.URL.Path)
		if err != nil {
			log.Println(err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		tile := renderTile(coords)
		if err := png.Encode(w, tile); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal server error: " + err.Error()))
			return
		}
	})
}

type TileCoords struct {
	Z, X, Y int
}

var pathRegex = regexp.MustCompile(`^/(\d+)/(\d+)/(\d+)\.png$`)

func extractTileCoords(path string) (TileCoords, error) {
	matches := pathRegex.FindStringSubmatch(path)
	if len(matches) != 4 {
		return TileCoords{}, fmt.Errorf("not enough matches, got %d", len(matches))
	}

	var coords TileCoords
	var err error
	coords.Z, err = strconv.Atoi(matches[1])
	if err != nil {
		return TileCoords{}, fmt.Errorf("extracting z: %v", err)
	}
	coords.X, err = strconv.Atoi(matches[2])
	if err != nil {
		return TileCoords{}, fmt.Errorf("extracting x: %v", err)
	}
	coords.Y, err = strconv.Atoi(matches[3])
	if err != nil {
		return TileCoords{}, fmt.Errorf("extracting y: %v", err)
	}

	max := 1 << uint(coords.Z)
	if coords.X < 0 || coords.X >= max || coords.Y < 0 || coords.Y >= max {
		return TileCoords{}, fmt.Errorf("invalid tile coordinates: %v", coords)
	}

	return coords, nil
}

const TilesTypeVert = "Vert"
const TilesTypeDSM = "Dsm"
const TilesTypeDEM = "Dem"

func renderTile(coords TileCoords) image.Image {
	apikey := os.Getenv("APIKEYPROD")
	//return NearmapTilesV3OverOpenStreetMap(coords.X, coords.Y, coords.Z, TilesTypeDEM, apikey, 2019)
	return NearmapTilesV3OverOpenStreetMap(coords.X, coords.Y, coords.Z, TilesTypeVert, apikey, 0)
}

func NearmapTilesV3OrOpenStreetMap(x, y, z int, tilestype, apikey string) image.Image {
	const tileSize = 256

	tile, err := GetImage(NearmapTilesV3JPEGURL(x, y, z, tilestype, apikey))
	if err != nil {
		tile, err = GetImage(OpenStreetMapURL(x, y, z))
	}
	if err != nil {
		tile = image.NewRGBA(image.Rect(0, 0, tileSize, tileSize))
	}
	return tile
}

func NearmapTilesV3OverOpenStreetMap(x, y, z int, tilestype, apikey string, year int) image.Image {
	const tileSize = 256
	r := image.Rect(0, 0, tileSize, tileSize)
	p := image.Pt(0, 0)

	url := NearmapTilesV3IMGURL(x, y, z, tilestype, apikey)
	if year >= 2000 {
		url = NearmapTilesV3DateIMGURL(x, y, z, tilestype, apikey, year)
	}
	lon, lat := NumToLonLat(x, y, z)
	fmt.Printf("fetching z=%d x=%d y=%d lon,lat=%f %f %s\n", z, x, y, lon, lat, url)

	nm, errNM := GetImage(url)
	if errNM == nil && imageIsOpaque(nm) {
		return nm // Already an opaque image, so just return it.
		// This avoids fetching any OpenStreetMap image, so is faster.
	}

    url2 := OpenStreetMapURL(x, y, z)
	fmt.Printf("fallback z=%d x=%d y=%d lon,lat=%f %f %s\n", z, x, y, lon, lat, url2)
	osm, errOSM := GetImage(url2)
	if errOSM != nil && errNM != nil {
		return image.NewRGBA(r) // Both images failed, return blank image.
	} else if errOSM != nil {
		return nm // No OpenStreetMap image, just return the Nearmap image.
	} else if errNM != nil {
		return osm // No Nearmap image, just return the OpenStreetMap image.
	}

	// Blend the images, with the OpenStreetMap image below the Nearmap image.
	tile := image.NewRGBA(r)
	draw.Draw(tile, r, osm, p, draw.Over)
	draw.Draw(tile, r, nm, p, draw.Over)
	return tile
}

func OpenStreetMapURL(x, y, z int) string {
	return fmt.Sprintf("https://tile.openstreetmap.org/%d/%d/%d.png", z, x, y)
}

func NearmapTilesV3JPEGURL(x, y, z int, tilestype, apikey string) string {
	return fmt.Sprintf("https://api.nearmap.com/tiles/v3/%s/%d/%d/%d.jpg?apikey=%s", tilestype, z, x, y, apikey)
}

func NearmapTilesV3PNGURL(x, y, z int, tilestype, apikey string) string {
	return fmt.Sprintf("https://api.nearmap.com/tiles/v3/%s/%d/%d/%d.png?apikey=%s", tilestype, z, x, y, apikey)
}

func NearmapTilesV3IMGURL(x, y, z int, tilestype, apikey string) string {
	return fmt.Sprintf("https://api.nearmap.com/tiles/v3/%s/%d/%d/%d.img?apikey=%s", tilestype, z, x, y, apikey)
}

func NearmapTilesV3DateIMGURL(x, y, z int, tilestype, apikey string, year int) string {
	return fmt.Sprintf("https://api.nearmap.com/tiles/v3/%s/%d/%d/%d.img?since=%04d-%02d-%02d&until=%04d-%02d-%02d&apikey=%s", tilestype, z, x, y, year, 1, 1, year, 12, 31, apikey)
}

func GetImage(url string) (image.Image, error) {
	response, err := http.Get(url)
	if err != nil || response.StatusCode != 200 {
		//fmt.Printf("error %d '%v' fetching %s\n", response.StatusCode, err, url)
		return nil, errors.New("error fetching image")
	}
	defer response.Body.Close()

	img, _, err := image.Decode(response.Body)
	if err != nil {
		//fmt.Printf("error '%v' decoding %s\n", err, url)
		return nil, err
	}

	return img, nil
}

func imageIsOpaque(img image.Image) bool {
	switch img.ColorModel() {
	case color.RGBAModel, color.RGBA64Model, color.NRGBAModel, color.NRGBA64Model,
		color.AlphaModel, color.Alpha16Model:
		return false
	default:
		return true
	}
}

// https://wiki.openstreetmap.org/wiki/Slippy_map_tilenames#Lon..2Flat._to_tile_numbers_2

func LonLatToNum(lon, lat float64, z int) (x int, y int) {
	x = int(math.Floor((lon + 180.0) / 360.0 * (math.Exp2(float64(z)))))
	y = int(math.Floor((1.0 - math.Log(math.Tan(lat*math.Pi/180.0)+1.0/math.Cos(lat*math.Pi/180.0))/math.Pi) / 2.0 * (math.Exp2(float64(z)))))
	return
}

func NumToLonLat(x, y, z int) (lon float64, lat float64) {
	n := math.Pi - 2.0*math.Pi*float64(y)/math.Exp2(float64(z))
	lon = float64(x)/math.Exp2(float64(z))*360.0 - 180.0
	lat = 180.0 / math.Pi * math.Atan(0.5*(math.Exp(n)-math.Exp(-n)))
	return lon, lat
}

type Vector struct {
	X, Y float64
}

func (v Vector) Sub(u Vector) Vector {
	return Vector{v.X - u.X, v.Y - u.Y}
}

func (v Vector) Scale(f float64) Vector {
	return Vector{v.X * f, v.Y * f}
}

type Extent struct {
	Min, Max Vector
}

func tileExtent(coords TileCoords) Extent {
	extent := Extent{
		Min: Vector{
			float64(coords.X) / float64(uint(1)<<uint(coords.Z)),
			float64(coords.Y) / float64(uint(1)<<uint(coords.Z)),
		},
		Max: Vector{
			float64(coords.X+1) / float64(uint(1)<<uint(coords.Z)),
			float64(coords.Y+1) / float64(uint(1)<<uint(coords.Z)),
		},
	}

	extent.Min = extent.Min.Sub(Vector{0.5, 0.5})
	extent.Max = extent.Max.Sub(Vector{0.5, 0.5})
	extent.Min = extent.Min.Scale(4)
	extent.Max = extent.Max.Scale(4)
	return extent
}
