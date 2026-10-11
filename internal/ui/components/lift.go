package components

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// This file is the recipe a published palette is turned into a table with
// (docs/interface/departures.md, "A published palette, lifted"): LiftToAA
// brings a short ink up to the floor in its own hue, and DistinctRungs gives
// every token a 256-colour rung of its own. Neither is read by the product at
// run time; they are what a person writing a table calls, and what the table's
// tests hold to the same bar every other table meets.

// aaFloor is WCAG's 4.5:1 for running text. The hints, key legend, counts and
// status line are instructions the interface gives, so every grey in a table is
// held to it on the table's ground and on its band
// (docs/interface/principles.md#a-colour-is-three-values-and-a-ground).
const aaFloor = 4.5

// liftStep is how far lightness moves between tries, in OKLab lightness
// (0 to 1): fine enough that the lifted value is the nearest one that clears
// the floor to within one step, coarse enough to finish in a few hundred
// tries.
const liftStep = 0.0025

// LiftToAA returns the value nearest to hex, in the same hue and chroma,
// that reaches 4.5:1 (WCAG 2) on both the ground and the band. It steps
// lightness in OKLCH away from the ground: lighter on a dark ground, darker
// on a light one. A hex that already clears the floor on both surfaces comes
// back as it was, lower-cased. Past the edge of sRGB the chroma is reduced,
// never the hue, and only as far as the value needs.
//
// It returns "" when a hex is not "#rrggbb" or when no value in that hue
// reaches the floor on both surfaces (a ground and a band no ink can stand
// off); a caller writing a table treats that as a table that cannot be made.
func LiftToAA(hex, ground, band string) string {
	c, ok := parseHex(hex)
	g, okG := parseHex(ground)
	b, okB := parseHex(band)
	if !ok || !okG || !okB {
		return ""
	}
	clears := func(v rgb) bool {
		return wcag(v, g) >= aaFloor && wcag(v, b) >= aaFloor
	}
	if clears(c) {
		return c.hex()
	}
	// Away from the ground: black and white stand off a luminance of about
	// 0.18 equally, so a ground darker than that is lifted and a lighter one
	// is darkened.
	dir := 1.0
	if relLuminance(g) >= 0.18 {
		dir = -1
	}
	l, ch, h := toOKLCH(c)
	for step := 1; ; step++ {
		nl := l + dir*liftStep*float64(step)
		if nl < 0 || nl > 1 {
			return ""
		}
		if v := fromOKLCH(nl, ch, h); clears(v) {
			return v.hex()
		}
	}
}

// DistinctRungs picks a 256-colour rung for each hex, in order, so that no
// two share one, and reports the worst RGB distance between a hex and the
// rung it was given. The pick is greedy: each hex in turn takes the nearest
// rung of the cube and the grey ramp (16 to 255) that an earlier hex has not
// taken. The order is the priority, so a caller lists the tokens that must be
// closest first (bright, body and the other greys, then the signals, the
// backgrounds, the band, the two tints, and the table's ground last, which
// has a 256 rung and no 16-colour one).
//
// A plain nearest pick for each hex collides on the greys: four greys a few
// points apart all land on the one ramp entry between them, and the ladder
// of a table loses its rungs at 256 colours.
//
// More than 240 hexes cannot be given distinct rungs; the rest come back as
// -1.
func DistinctRungs(hexes []string) (rungs []int, worst float64) {
	taken := map[int]bool{}
	rungs = make([]int, len(hexes))
	for i, hx := range hexes {
		best, bestD := nearestFree(hx, taken)
		rungs[i] = best
		if best >= 0 {
			taken[best] = true
			worst = math.Max(worst, bestD)
		}
	}
	return rungs, worst
}

// nearestFree is the rung of 16 to 255 nearest hex that is not in taken, and
// its distance; -1 when hex is not a colour or every rung is taken.
func nearestFree(hex string, taken map[int]bool) (int, float64) {
	c, ok := parseHex(hex)
	if !ok {
		return -1, 0
	}
	best, bestD := -1, math.MaxFloat64
	for idx := 16; idx <= 255; idx++ {
		if taken[idx] {
			continue
		}
		if d := dist(c, xterm256(idx)); d < bestD {
			best, bestD = idx, d
		}
	}
	if best < 0 {
		return -1, 0
	}
	return best, bestD
}

// xterm256 is the colour a 256-colour terminal paints for index 16 to 255:
// the 6x6x6 cube at 0, 95, 135, 175, 215, 255, then 24 greys from 8 in
// steps of 10.
func xterm256(idx int) rgb {
	if idx >= 232 {
		v := uint8(8 + 10*(idx-232))
		return rgb{v, v, v}
	}
	level := func(n int) uint8 {
		if n == 0 {
			return 0
		}
		return uint8(55 + 40*n)
	}
	n := idx - 16
	return rgb{level(n / 36), level(n / 6 % 6), level(n % 6)}
}

type rgb struct{ r, g, b uint8 }

func (c rgb) hex() string { return fmt.Sprintf("#%02x%02x%02x", c.r, c.g, c.b) }

func parseHex(s string) (rgb, bool) {
	if len(s) != 7 || s[0] != '#' {
		return rgb{}, false
	}
	n, err := strconv.ParseUint(strings.ToLower(s[1:]), 16, 32)
	if err != nil {
		return rgb{}, false
	}
	return rgb{uint8(n >> 16), uint8(n >> 8), uint8(n)}, true
}

func dist(a, b rgb) float64 {
	dr, dg, db := float64(a.r)-float64(b.r), float64(a.g)-float64(b.g), float64(a.b)-float64(b.b)
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

func linear(v uint8) float64 {
	x := float64(v) / 255
	if x <= 0.03928 {
		return x / 12.92
	}
	return math.Pow((x+0.055)/1.055, 2.4)
}

func relLuminance(c rgb) float64 {
	return 0.2126*linear(c.r) + 0.7152*linear(c.g) + 0.0722*linear(c.b)
}

// wcag is the WCAG 2 contrast ratio, the formula the palette tests measure
// with.
func wcag(a, b rgb) float64 {
	hi, lo := relLuminance(a), relLuminance(b)
	if hi < lo {
		hi, lo = lo, hi
	}
	return (hi + 0.05) / (lo + 0.05)
}

// toOKLCH converts an sRGB colour to OKLCH (Ottosson, 2020): lightness 0 to
// 1, chroma, and hue in degrees.
func toOKLCH(c rgb) (l, ch, h float64) {
	r, g, b := linear(c.r), linear(c.g), linear(c.b)
	lc := math.Cbrt(0.4122214708*r + 0.5363325363*g + 0.0514459929*b)
	mc := math.Cbrt(0.2119034982*r + 0.6806995451*g + 0.1073969566*b)
	sc := math.Cbrt(0.0883024619*r + 0.2817188376*g + 0.6299787005*b)
	l = 0.2104542553*lc + 0.7936177850*mc - 0.0040720468*sc
	a := 1.9779984951*lc - 2.4285922050*mc + 0.4505937099*sc
	bb := 0.0259040371*lc + 0.7827717662*mc - 0.8086757660*sc
	ch = math.Hypot(a, bb)
	h = math.Atan2(bb, a) * 180 / math.Pi
	if h < 0 {
		h += 360
	}
	return l, ch, h
}

// fromOKLCH is the way back, rounded to the nearest sRGB value. A colour past
// the edge of the gamut keeps its lightness and hue and gives up chroma, by
// bisection, until it is inside.
func fromOKLCH(l, ch, h float64) rgb {
	if v, ok := oklchToLinear(l, ch, h); ok {
		return encode(v)
	}
	lo, hi := 0.0, ch
	for i := 0; i < 24; i++ {
		mid := (lo + hi) / 2
		if _, ok := oklchToLinear(l, mid, h); ok {
			lo = mid
		} else {
			hi = mid
		}
	}
	v, _ := oklchToLinear(l, lo, h)
	return encode(v)
}

// gamutSlack is how far past 0 or 1 a linear channel may sit and still round
// to a real sRGB value.
const gamutSlack = 0.0005

func oklchToLinear(l, ch, h float64) ([3]float64, bool) {
	a := ch * math.Cos(h*math.Pi/180)
	b := ch * math.Sin(h*math.Pi/180)
	cube := func(x float64) float64 { return x * x * x }
	lc := cube(l + 0.3963377774*a + 0.2158037573*b)
	mc := cube(l - 0.1055613458*a - 0.0638541728*b)
	sc := cube(l - 0.0894841775*a - 1.2914855480*b)
	v := [3]float64{
		4.0767416621*lc - 3.3077115913*mc + 0.2309699292*sc,
		-1.2684380046*lc + 2.6097574011*mc - 0.3413193965*sc,
		-0.0041960863*lc - 0.7034186147*mc + 1.7076147010*sc,
	}
	for _, x := range v {
		if x < -gamutSlack || x > 1+gamutSlack {
			return v, false
		}
	}
	return v, true
}

func encode(v [3]float64) rgb {
	one := func(x float64) uint8 {
		x = math.Min(1, math.Max(0, x))
		if x <= 0.0031308 {
			x *= 12.92
		} else {
			x = 1.055*math.Pow(x, 1/2.4) - 0.055
		}
		return uint8(math.Round(x * 255))
	}
	return rgb{one(v[0]), one(v[1]), one(v[2])}
}
