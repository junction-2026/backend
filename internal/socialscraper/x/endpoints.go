package x

// Everything X can change without notice lives in this file: the GraphQL query ids (they rotate when the web
// app is redeployed), the feature flags each operation expects, and the public web-app bearer. When X answers
// 400/404 for an operation, refresh the id from the web client's main.js bundle (`queryId:"...",operationName:"..."`).

const (
	graphqlBase = "https://x.com/i/api/graphql/"

	// bearer is the public token embedded in the X web app; it identifies the app, not a user.
	bearer = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"
)

type operation struct {
	ID           string
	Name         string
	Features     map[string]bool
	FieldToggles map[string]bool
}

var (
	opUserByScreenName = operation{ID: "xc8f1g7BYqr6VTzTbvNlGw", Name: "UserByScreenName", Features: features,
		FieldToggles: map[string]bool{"withAuxiliaryUserLabels": false}}
	opUserTweets = operation{ID: "E3opETHurmVJflFsUBVuUQ", Name: "UserTweets", Features: features,
		FieldToggles: map[string]bool{"withArticlePlainText": false}}
	opTweetDetail = operation{ID: "xOhkmRac04YFZmOzU9PJHg", Name: "TweetDetail", Features: features,
		FieldToggles: map[string]bool{"withArticleRichContentState": true, "withArticlePlainText": false}}
)

// features are the flags the web client sends with its GraphQL calls.
var features = map[string]bool{
	"articles_preview_enabled":                                                true,
	"c9s_tweet_anatomy_moderator_badge_enabled":                               true,
	"communities_web_enable_tweet_community_results_fetch":                    true,
	"creator_subscriptions_quote_tweet_preview_enabled":                       false,
	"creator_subscriptions_tweet_preview_api_enabled":                         true,
	"freedom_of_speech_not_reach_fetch_enabled":                               true,
	"graphql_is_translatable_rweb_tweet_is_translatable_enabled":              true,
	"hidden_profile_subscriptions_enabled":                                    true,
	"highlights_tweets_tab_ui_enabled":                                        true,
	"longform_notetweets_consumption_enabled":                                 true,
	"longform_notetweets_inline_media_enabled":                                true,
	"longform_notetweets_rich_text_read_enabled":                              true,
	"profile_label_improvements_pcf_label_in_post_enabled":                    true,
	"responsive_web_edit_tweet_api_enabled":                                   true,
	"responsive_web_enhance_cards_enabled":                                    false,
	"responsive_web_graphql_exclude_directive_enabled":                        true,
	"responsive_web_graphql_skip_user_profile_image_extensions_enabled":       false,
	"responsive_web_graphql_timeline_navigation_enabled":                      true,
	"responsive_web_twitter_article_tweet_consumption_enabled":                true,
	"rweb_tipjar_consumption_enabled":                                         true,
	"rweb_video_timestamps_enabled":                                           true,
	"standardized_nudges_misinfo":                                             true,
	"subscriptions_verification_info_is_identity_verified_enabled":            true,
	"subscriptions_verification_info_verified_since_enabled":                  true,
	"tweet_with_visibility_results_prefer_gql_limited_actions_policy_enabled": true,
	"tweetypie_unmention_optimization_enabled":                                true,
	"verified_phone_label_enabled":                                            false,
	"view_counts_everywhere_api_enabled":                                      true,
}
