package adapter

// ReasonNoSearchableTopics is the skip reason of news_search when topic_extraction found nothing to search
// (defined here, not in internal/job).
const ReasonNoSearchableTopics = "no_searchable_topics"

// Skip is returned by an adapter that decides at run time that it has nothing to do: the engine settles the
// running analyzer as skipped with Reason and no result.
type Skip struct{ Reason string }

func (s *Skip) Error() string { return "analyzer skipped: " + s.Reason }
