package apitypes

import "time"

type Bot struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Role        string    `json:"role"`
	Channel     string    `json:"channel"`
	AgentID     string    `json:"agent_id"`
	UserID      string    `json:"user_id"`
	Available   bool      `json:"available"`
	CreatedAt   time.Time `json:"created_at"`
}

type CreateBotRequest struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Role        string `json:"role"`
	Channel     string `json:"channel,omitempty"`
	ModelID     string `json:"model_id,omitempty"`
	RuntimeMode string `json:"runtime_mode,omitempty"`
}

type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Handle    string    `json:"handle"`
	Role      string    `json:"role"`
	Avatar    string    `json:"avatar"`
	IsOnline  bool      `json:"is_online"`
	LastSeen  string    `json:"last_seen,omitempty"`
	AccentHex string    `json:"accent_hex"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

type Message struct {
	ID        string        `json:"id"`
	SenderID  string        `json:"sender_id"`
	Kind      string        `json:"kind,omitempty"`
	Content   string        `json:"content"`
	Event     *EventPayload `json:"event,omitempty"`
	CreatedAt time.Time     `json:"created_at"`
	Mentions  []string      `json:"mentions"`
}

type CreateMessageRequest struct {
	RoomID    string `json:"room_id"`
	SenderID  string `json:"sender_id"`
	Content   string `json:"content"`
	MentionID string `json:"mention_id,omitempty"`
}

type EventPayload struct {
	Key       string   `json:"key"`
	ActorID   string   `json:"actor_id,omitempty"`
	Title     string   `json:"title,omitempty"`
	TargetIDs []string `json:"target_ids,omitempty"`
}

type Room struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Subtitle     string    `json:"subtitle"`
	Description  string    `json:"description,omitempty"`
	Participants []string  `json:"participants"`
	Messages     []Message `json:"messages"`
}

type CreateRoomRequest struct {
	Title          string   `json:"title"`
	Description    string   `json:"description"`
	CreatorID      string   `json:"creator_id"`
	ParticipantIDs []string `json:"participant_ids"`
	Locale         string   `json:"locale"`
}

type AddRoomMembersRequest struct {
	RoomID    string   `json:"room_id,omitempty"`
	InviterID string   `json:"inviter_id"`
	UserIDs   []string `json:"user_ids"`
	Locale    string   `json:"locale"`
}
