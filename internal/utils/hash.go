package utils

type Hash struct {
	Algo string
	Hash string
}

func (h Hash) String() string {
	return h.Algo + ":" + h.Hash
}
