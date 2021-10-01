package gpstrail

import (
	"math"
	"time"
)

const (
	trailTTL       time.Duration = -5 * time.Minute
	markerDistance float64       = 0.2
)

type GpsTrail struct {
	inbox chan interface{}
}

type Position struct {
	TenantID  string    `json:"-"`
	Device    string    `json:"device"`
	Timestamp time.Time `json:"timestamp"`
	Lat       float64   `json:"lat"`
	Lon       float64   `json:"lon"`
}

type add struct {
	ID       string
	TenantID string
	Inbox    chan<- []*Position
	Close    func()
}

type remove struct {
	ID string
}

func (gt *GpsTrail) Subscribe(id string, tenantID string, inbox chan<- []*Position, close func()) {
	gt.inbox <- &add{id, tenantID, inbox, close}
}

func (gt *GpsTrail) Unsubscribe(id string) {
	gt.inbox <- &remove{id}
}

func (gt *GpsTrail) Post(pos *Position) {
	gt.inbox <- pos
}

func New() *GpsTrail {
	inbox := make(chan interface{}, 10)
	go func() {
		latestPositions := make(map[string]*Position)
		queue := make([]*Position, 0)
		subscribers := make(map[string]*add)
		for x := range inbox {
			switch m := x.(type) {
			case *add:
				subscribers[m.ID] = m
				m.Inbox <- tenantFilter(queue, m.TenantID)
			case *remove:
				delete(subscribers, m.ID)
			case *Position:
				t := time.Now().Add(trailTTL)
				latest, found := latestPositions[m.Device]
				if !found || addToTrail(latest, m, t) {
					latestPositions[m.Device] = m
					queue = append(queue, m)
					for _, v := range subscribers {
						if m.TenantID != v.TenantID {
							continue
						}
						select {
						case v.Inbox <- []*Position{m}:
						default:
							v.Close()
						}
					}
				}

				// Remove old trail markers
				i := 0
				for j, x := range queue {
					if x.Timestamp.Before(t) {
						queue[j] = nil
						i = j + 1
					} else {
						break
					}
				}
				queue = queue[i:]
			}
		}
	}()

	return &GpsTrail{inbox}
}

func addToTrail(previous, current *Position, t time.Time) bool {
	return previous.Timestamp.Before(t) ||
		distance(previous.Lon, previous.Lat, current.Lon, current.Lat) > markerDistance
}

func tenantFilter(source []*Position, tenantID string) []*Position {
	dest := make([]*Position, 0)
	for _, x := range source {
		if x.TenantID == tenantID {
			dest = append(dest, x)
		}
	}

	return dest
}

const earthRadiusMetres float64 = 6371000

// https://play.golang.org/p/MZVh5bRWqN
func distance(lonFrom float64, latFrom float64, lonTo float64, latTo float64) float64 {
	var deltaLat = (latTo - latFrom) * (math.Pi / 180)
	var deltaLon = (lonTo - lonFrom) * (math.Pi / 180)

	var a = math.Sin(deltaLat/2)*math.Sin(deltaLat/2) +
		math.Cos(latFrom*(math.Pi/180))*math.Cos(latTo*(math.Pi/180))*
			math.Sin(deltaLon/2)*math.Sin(deltaLon/2)
	var c = 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))

	return earthRadiusMetres * c
}
