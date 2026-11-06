package store

// behaviour is a channel a dream gave a global fact before the card went (#625, #656); the tests write it to check
// that an older label survives the text history, the cache and a merge.
const behaviour = "behaviour"

// setChannel adds a version of an active fact with a channel, as the dream of #625 did.
func (s *Store) setChannel(id string, version int, channel string) (Fact, error) {
	cur, err := s.Get(id)
	if err != nil {
		return Fact{}, err
	}
	f := cur
	f.Version, f.Author, f.Channel = cur.Version+1, s.author, channel
	return s.write(f)
}
