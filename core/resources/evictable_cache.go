package resources

type EvictableCache interface {
	Name() string
	Size() int64
	EvictPercent(percent float64) int64
}
