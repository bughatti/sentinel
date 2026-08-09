// Package pipeline implements the per-camera event state machine, zone
// filtering, and object tracking.
package pipeline

import (
	"fmt"
	"strconv"
	"strings"
)

// Zone represents a named polygon region within a camera frame.
// Coordinates are in normalised [0, 1] space relative to frame width/height.
type Zone struct {
	Name    string
	Objects []string // empty = all objects
	Inertia int      // frames the object must be inside before zone triggers

	// polygon stores the zone vertices as (x, y) pairs.
	polygon [][2]float32
}

// NewZone parses a coordinates string of the form "x1,y1,x2,y2,..." and
// returns a Zone. Returns an error if fewer than 3 vertices are provided or
// the string is malformed.
func NewZone(name, coordinates string, objects []string, inertia int) (*Zone, error) {
	parts := strings.Split(strings.TrimSpace(coordinates), ",")
	if len(parts)%2 != 0 {
		return nil, fmt.Errorf("zone %q: odd number of coordinate values", name)
	}
	if len(parts) < 6 {
		return nil, fmt.Errorf("zone %q: need at least 3 vertices (6 values), got %d", name, len(parts))
	}

	polygon := make([][2]float32, len(parts)/2)
	for i := 0; i < len(parts); i += 2 {
		x, err := strconv.ParseFloat(strings.TrimSpace(parts[i]), 32)
		if err != nil {
			return nil, fmt.Errorf("zone %q: parse x[%d]: %w", name, i/2, err)
		}
		y, err := strconv.ParseFloat(strings.TrimSpace(parts[i+1]), 32)
		if err != nil {
			return nil, fmt.Errorf("zone %q: parse y[%d]: %w", name, i/2, err)
		}
		polygon[i/2] = [2]float32{float32(x), float32(y)}
	}

	if inertia < 1 {
		inertia = 1
	}

	return &Zone{
		Name:    name,
		Objects: objects,
		Inertia: inertia,
		polygon: polygon,
	}, nil
}

// Contains returns true if the point (x, y) lies inside (or on the boundary
// of) the zone polygon. Uses the ray-casting algorithm.
func (z *Zone) Contains(x, y float32) bool {
	n := len(z.polygon)
	if n < 3 {
		return false
	}
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		xi, yi := z.polygon[i][0], z.polygon[i][1]
		xj, yj := z.polygon[j][0], z.polygon[j][1]

		if ((yi > y) != (yj > y)) &&
			(x < (xj-xi)*(y-yi)/(yj-yi)+xi) {
			inside = !inside
		}
		j = i
	}
	return inside
}

// TracksObject returns true if this zone cares about the given label (i.e.
// the zone has no object filter, or label is in the filter list).
func (z *Zone) TracksObject(label string) bool {
	if len(z.Objects) == 0 {
		return true
	}
	for _, o := range z.Objects {
		if o == label {
			return true
		}
	}
	return false
}

// ContainsBox returns true if the centre-point of the box lies inside the zone.
// box values are normalised (0..1).
func (z *Zone) ContainsBox(x1, y1, x2, y2 float32) bool {
	cx := (x1 + x2) / 2
	cy := (y1 + y2) / 2
	return z.Contains(cx, cy)
}
