package minhashlsh

import (
	"compress/gzip"
	"encoding/binary"
	"encoding/gob"
	"math"
	"os"
	"sort"
)

const (
	integrationPrecision = 0.01
)

type hashKeyFunc func([]uint64) string

func hashKeyFuncGen(hashValueSize int) hashKeyFunc {
	return func(sig []uint64) string {
		s := make([]byte, hashValueSize*len(sig))
		buf := make([]byte, 8)
		for i, v := range sig {
			binary.LittleEndian.PutUint64(buf, v)
			copy(s[i*hashValueSize:(i+1)*hashValueSize], buf[:hashValueSize])
		}
		return string(s)
	}
}

// Compute the integral of function f, lower limit a, upper limit L, and
// precision defined as the quantize step
func integral(f func(float64) float64, a, b, precision float64) float64 {
	var area float64
	for x := a; x < b; x += precision {
		area += f(x+0.5*precision) * precision
	}
	return area
}

// Probability density function for false positive
func falsePositive(l, k int) func(float64) float64 {
	return func(j float64) float64 {
		return 1.0 - math.Pow(1.0-math.Pow(j, float64(k)), float64(l))
	}
}

// Probability density function for false negative
func falseNegative(l, k int) func(float64) float64 {
	return func(j float64) float64 {
		return 1.0 - (1.0 - math.Pow(1.0-math.Pow(j, float64(k)), float64(l)))
	}
}

// Compute the cummulative probability of false negative given threshold t
func probFalseNegative(l, k int, t, precision float64) float64 {
	return integral(falseNegative(l, k), t, 1.0, precision)
}

// Compute the cummulative probability of false positive given threshold t
func probFalsePositive(l, k int, t, precision float64) float64 {
	return integral(falsePositive(l, k), 0, t, precision)
}

// optimalKL returns the optimal K and L for Jaccard similarity search,
// and the false positive and negative probabilities.
// t is the Jaccard similarity threshold.
func optimalKL(numHash int, t float64) (optK, optL int, fp, fn float64) {
	minError := math.MaxFloat64
	for l := 1; l <= numHash; l++ {
		for k := 1; k <= numHash; k++ {
			if l*k > numHash {
				continue
			}
			currFp := probFalsePositive(l, k, t, integrationPrecision)
			currFn := probFalseNegative(l, k, t, integrationPrecision)
			currErr := currFn + currFp
			if minError > currErr {
				minError = currErr
				optK = k
				optL = l
				fp = currFp
				fn = currFn
			}
		}
	}
	return
}

// entry contains the hash Key (from minhash signature) and the indexed Key
type entry struct {
	HashKey string
	Key     interface{}
}

// hashTable is a look-up table implemented as a slice sorted by hash keys.
// Look-up operation is implemented using binary search.
type hashTable []entry

func (h hashTable) Len() int           { return len(h) }
func (h hashTable) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h hashTable) Less(i, j int) bool { return h[i].HashKey < h[j].HashKey }

// MinhashLSH represents a MinHash LSH implemented using LSH Forest
// (http://ilpubs.stanford.edu:8090/678/1/2005-14.pdf).
// It supports query-time setting of the MinHash LSH parameters
// L (number of bands) and
// K (number of hash functions per band).
type MinhashLSH struct {
	K              int
	L              int
	HashTables     []hashTable
	HashKeyFunc    hashKeyFunc
	HashValueSize  int
	NumIndexedKeys int
}

// Save MinHash LSH index
func (minhashLsh *MinhashLSH) Save(filename string) error {
	fi, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer fi.Close()

	fz := gzip.NewWriter(fi)
	defer fz.Close()

	encoder := gob.NewEncoder(fz)
	err = encoder.Encode(*minhashLsh)
	if err != nil {
		return err
	}

	return nil
}

// Load MinHash LSH index
func Load(filename string) (*MinhashLSH, error) {

	fi, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer fi.Close()

	fz, err := gzip.NewReader(fi)
	if err != nil {
		return nil, err
	}
	defer fz.Close()

	decoder := gob.NewDecoder(fz)
	lshIndex := new(MinhashLSH)
	err = decoder.Decode(lshIndex)
	if err != nil {
		return nil, err
	}

	lshIndex.HashKeyFunc = hashKeyFuncGen(lshIndex.HashValueSize)

	return lshIndex, nil
}

func newMinhashLSH(threshold float64, numHash, hashValueSize, initSize int) *MinhashLSH {
	k, l, _, _ := optimalKL(numHash, threshold)
	hashTables := make([]hashTable, l)
	for i := range hashTables {
		hashTables[i] = make(hashTable, 0, initSize)
	}
	return &MinhashLSH{
		K:              k,
		L:              l,
		HashValueSize:  hashValueSize,
		HashTables:     hashTables,
		HashKeyFunc:    hashKeyFuncGen(hashValueSize),
		NumIndexedKeys: 0,
	}
}

// NewMinhashLSH64 uses 64-bit hash values and pre-allocation of hash tables.
func NewMinhashLSH64(numHash int, threshold float64, initSize int) *MinhashLSH {
	return newMinhashLSH(threshold, numHash, 8, initSize)
}

// NewMinhashLSH32 uses 32-bit hash values and pre-allocation of hash tables.
// MinHash signatures with 64 bit hash values will have
// their hash values trimed.
func NewMinhashLSH32(numHash int, threshold float64, initSize int) *MinhashLSH {
	return newMinhashLSH(threshold, numHash, 4, initSize)
}

// NewMinhashLSH16 uses 16-bit hash values and pre-allocation of hash tables.
// MinHash signatures with 64 or 32 bit hash values will have
// their hash values trimed.
func NewMinhashLSH16(numHash int, threshold float64, initSize int) *MinhashLSH {
	return newMinhashLSH(threshold, numHash, 2, initSize)
}

// NewMinhashLSH is the default constructor uses 32 bit hash value
// with pre-allocation of hash tables.
var NewMinhashLSH = NewMinhashLSH32

// Params returns the LSH parameters K and L
func (f *MinhashLSH) Params() (k, l int) {
	return f.K, f.L
}

func (f *MinhashLSH) hashKeys(sig []uint64) []string {
	hs := make([]string, f.L)
	for i := 0; i < f.L; i++ {
		hs[i] = f.HashKeyFunc(sig[i*f.K : (i+1)*f.K])
	}
	return hs
}

// Add a Key with MinHash signature into the index.
// The Key won't be searchable until Index() is called.
func (f *MinhashLSH) Add(key interface{}, sig []uint64) {
	// Generate hash keys
	hs := f.hashKeys(sig)
	// Insert keys into the hash tables by appending.
	for i := range f.HashTables {
		f.HashTables[i] = append(f.HashTables[i], entry{hs[i], key})
	}
}

// Index makes all the keys added searchable.
func (f *MinhashLSH) Index() {
	for i := range f.HashTables {
		sort.Sort(f.HashTables[i])
	}
	f.NumIndexedKeys = len(f.HashTables[0])
}

// Query returns candidate keys given the query signature.
func (f *MinhashLSH) Query(sig []uint64) []interface{} {
	set := f.query(sig)
	results := make([]interface{}, 0, len(set))
	for key := range set {
		results = append(results, key)
	}
	return results
}

func (f *MinhashLSH) query(sig []uint64) map[interface{}]bool {
	// Generate hash keys.
	hashKeys := f.hashKeys(sig)
	results := make(map[interface{}]bool)
	// Query hash tables using binary search.
	for i := 0; i < f.L; i++ {
		// Only search over the indexed keys.
		hashTable := f.HashTables[i][:f.NumIndexedKeys]
		hashKey := hashKeys[i]
		k := sort.Search(len(hashTable), func(x int) bool {
			return hashTable[x].HashKey >= hashKey
		})
		if k < len(hashTable) && hashTable[k].HashKey == hashKey {
			for j := k; j < len(hashTable) && hashTable[j].HashKey == hashKey; j++ {
				key := hashTable[j].Key
				if _, exist := results[key]; !exist {
					results[key] = true
				}
			}
		}
	}
	return results
}
