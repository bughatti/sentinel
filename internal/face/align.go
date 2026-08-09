package face

import "math"

// arcfaceTemplate is the canonical 5-point landmark layout ArcFace expects on a
// 112x112 aligned crop (left-eye, right-eye, nose, left-mouth, right-mouth).
var arcfaceTemplate = [5][2]float64{
	{38.2946, 51.6963},
	{73.5318, 51.5014},
	{56.0252, 71.7366},
	{41.5493, 92.3655},
	{70.7299, 92.2041},
}

const arcfaceSize = 112

// umeyama computes the 2x3 similarity transform (scale*R | t) mapping the given
// source landmarks onto arcfaceTemplate. Mirrors scikit-image's _umeyama.
func umeyama(src [5][2]float64) [2][3]float64 {
	const n = 5
	var srcMean, dstMean [2]float64
	for i := 0; i < n; i++ {
		srcMean[0] += src[i][0]
		srcMean[1] += src[i][1]
		dstMean[0] += arcfaceTemplate[i][0]
		dstMean[1] += arcfaceTemplate[i][1]
	}
	srcMean[0] /= n
	srcMean[1] /= n
	dstMean[0] /= n
	dstMean[1] /= n

	// A = (1/n) * sum (dst_demean_i outer src_demean_i)  -> 2x2
	var A [2][2]float64
	var srcVar float64
	for i := 0; i < n; i++ {
		sdx := src[i][0] - srcMean[0]
		sdy := src[i][1] - srcMean[1]
		ddx := arcfaceTemplate[i][0] - dstMean[0]
		ddy := arcfaceTemplate[i][1] - dstMean[1]
		A[0][0] += ddx * sdx
		A[0][1] += ddx * sdy
		A[1][0] += ddy * sdx
		A[1][1] += ddy * sdy
		srcVar += sdx*sdx + sdy*sdy
	}
	A[0][0] /= n
	A[0][1] /= n
	A[1][0] /= n
	A[1][1] /= n
	srcVar /= n

	U, s, Vt := svd2x2(A)

	d0, d1 := 1.0, 1.0
	if det2x2(A) < 0 {
		d1 = -1
	}

	// R = U * diag(d) * Vt
	var R [2][2]float64
	// diag(d)*Vt
	dv := [2][2]float64{
		{d0 * Vt[0][0], d0 * Vt[0][1]},
		{d1 * Vt[1][0], d1 * Vt[1][1]},
	}
	R[0][0] = U[0][0]*dv[0][0] + U[0][1]*dv[1][0]
	R[0][1] = U[0][0]*dv[0][1] + U[0][1]*dv[1][1]
	R[1][0] = U[1][0]*dv[0][0] + U[1][1]*dv[1][0]
	R[1][1] = U[1][0]*dv[0][1] + U[1][1]*dv[1][1]

	scale := 1.0
	if srcVar > 1e-12 {
		scale = (s[0]*d0 + s[1]*d1) / srcVar
	}

	var M [2][3]float64
	M[0][0] = scale * R[0][0]
	M[0][1] = scale * R[0][1]
	M[1][0] = scale * R[1][0]
	M[1][1] = scale * R[1][1]
	M[0][2] = dstMean[0] - scale*(R[0][0]*srcMean[0]+R[0][1]*srcMean[1])
	M[1][2] = dstMean[1] - scale*(R[1][0]*srcMean[0]+R[1][1]*srcMean[1])
	return M
}

func det2x2(a [2][2]float64) float64 { return a[0][0]*a[1][1] - a[0][1]*a[1][0] }

// svd2x2 returns U, singular values s (descending, >=0), and Vt for a 2x2
// matrix, via the symmetric eigendecomposition of AᵀA. A = U diag(s) Vt.
func svd2x2(A [2][2]float64) (U [2][2]float64, s [2]float64, Vt [2][2]float64) {
	a, b := A[0][0], A[0][1]
	c, d := A[1][0], A[1][1]
	// AtA (symmetric)
	m00 := a*a + c*c
	m01 := a*b + c*d
	m11 := b*b + d*d

	tr := m00 + m11
	dt := m00*m11 - m01*m01
	disc := math.Sqrt(math.Max(tr*tr/4-dt, 0))
	l0 := tr/2 + disc
	l1 := tr/2 - disc
	s[0] = math.Sqrt(math.Max(l0, 0))
	s[1] = math.Sqrt(math.Max(l1, 0))

	// eigenvector of AtA for l0 → first right-singular vector (column of V)
	var v0 [2]float64
	if math.Abs(m01) > 1e-12 {
		v0 = [2]float64{l0 - m11, m01}
	} else if m00 >= m11 {
		v0 = [2]float64{1, 0}
	} else {
		v0 = [2]float64{0, 1}
	}
	n0 := math.Hypot(v0[0], v0[1])
	if n0 > 0 {
		v0[0] /= n0
		v0[1] /= n0
	}
	v1 := [2]float64{-v0[1], v0[0]} // perpendicular

	// V columns = v0, v1  → Vt rows = v0, v1
	Vt[0][0], Vt[0][1] = v0[0], v0[1]
	Vt[1][0], Vt[1][1] = v1[0], v1[1]

	// U columns = A*v_i / s_i
	uc := func(v [2]float64, sv float64) [2]float64 {
		av := [2]float64{a*v[0] + b*v[1], c*v[0] + d*v[1]}
		if sv > 1e-12 {
			return [2]float64{av[0] / sv, av[1] / sv}
		}
		return av
	}
	u0 := uc(v0, s[0])
	u1 := uc(v1, s[1])
	U[0][0], U[1][0] = u0[0], u0[1]
	U[0][1], U[1][1] = u1[0], u1[1]
	return U, s, Vt
}

// invertAffine inverts a 2x3 affine [ [m00,m01,m02],[m10,m11,m12] ].
func invertAffine(M [2][3]float64) [2][3]float64 {
	det := M[0][0]*M[1][1] - M[0][1]*M[1][0]
	if math.Abs(det) < 1e-12 {
		det = 1e-12
	}
	id := 1.0 / det
	var inv [2][3]float64
	inv[0][0] = M[1][1] * id
	inv[0][1] = -M[0][1] * id
	inv[1][0] = -M[1][0] * id
	inv[1][1] = M[0][0] * id
	inv[0][2] = -(inv[0][0]*M[0][2] + inv[0][1]*M[1][2])
	inv[1][2] = -(inv[1][0]*M[0][2] + inv[1][1]*M[1][2])
	return inv
}

// rgbSampler returns the bilinearly-interpolated (r,g,b) at fractional (x,y) in
// source-image pixel space, in RGB order, 0..255. Out-of-bounds returns 0.
type rgbSampler func(x, y float64) (r, g, b float32)

// buildArcfaceInput warps the source (via the 5-point alignment for kps) into a
// 112x112 aligned face and fills an NCHW float32 tensor with insightface's
// ArcFace normalisation ((v-127.5)/127.5, RGB order). len(out) must be 3*112*112.
func buildArcfaceInput(sample rgbSampler, kps [5][2]float32, out []float32) {
	var src [5][2]float64
	for i := 0; i < 5; i++ {
		src[i][0] = float64(kps[i][0])
		src[i][1] = float64(kps[i][1])
	}
	M := umeyama(src)     // src(image) -> dst(112 template)
	inv := invertAffine(M) // dst(112) -> src(image), for sampling
	const sz = arcfaceSize
	plane := sz * sz
	for v := 0; v < sz; v++ {
		for u := 0; u < sz; u++ {
			sx := inv[0][0]*float64(u) + inv[0][1]*float64(v) + inv[0][2]
			sy := inv[1][0]*float64(u) + inv[1][1]*float64(v) + inv[1][2]
			r, g, b := sample(sx, sy)
			idx := v*sz + u
			out[idx] = (r - 127.5) / 127.5          // R
			out[plane+idx] = (g - 127.5) / 127.5    // G
			out[2*plane+idx] = (b - 127.5) / 127.5  // B
		}
	}
}

// l2normalize normalises a vector in place and returns it.
func l2normalize(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	n := math.Sqrt(sum)
	if n > 0 {
		inv := float32(1.0 / n)
		for i := range v {
			v[i] *= inv
		}
	}
	return v
}
