package box2

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEncodeList(t *testing.T) {
	type testcase struct {
		name string
		in   [][]byte
		out  []byte
	}

	tcs := []testcase{
		{
			name: "empty",
		},
		{
			name: "single",
			in:   [][]byte{[]byte("asd")},
			out:  []byte{3, 0, 'a', 's', 'd'},
		},
		{
			name: "single-binary",
			in:   [][]byte{[]byte{0, 8, 16, 32}},
			out:  []byte{4, 0, 0, 8, 16, 32},
		},
		{
			name: "pair",
			in:   [][]byte{[]byte("asd"), []byte("def")},
			out:  []byte{3, 0, 'a', 's', 'd', 3, 0, 'd', 'e', 'f'},
		},
		{
			name: "pair-binary",
			in:   [][]byte{[]byte{0, 8, 16, 32}, []byte{4, 12, 20, 36}},
			out:  []byte{4, 0, 0, 8, 16, 32, 4, 0, 4, 12, 20, 36},
		},
	}

	for _, tc := range tcs {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.out, EncodeSLP(nil, tc.in...))
		})
	}
}
