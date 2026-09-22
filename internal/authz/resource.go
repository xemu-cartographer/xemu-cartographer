package authz

// ResourceKind classifies what an action is applied to. rules.go records the
// kind(s) each action accepts and Can denies a mismatch ("resource") so a
// call site that passes the wrong constructor can never reach an
// unconditional predicate by accident.
type ResourceKind string

const (
	ResGlobal    ResourceKind = "global"    // selectorless actions (overlay.list_consoles, container.list, token.list, library.manage)
	ResInstance  ResourceKind = "instance"  // scraper runner / container name (same namespace)
	ResContainer ResourceKind = "container" // box actions; ID = container name
	ResRoom      ResourceKind = "room"
	ResUser      ResourceKind = "user"     // ID = users.id
	ResRole      ResourceKind = "role"     // ID = roles.slug; Extra["target_user"] for grant/revoke
	ResTeam      ResourceKind = "team"     // ID = teams.id
	ResRecord    ResourceKind = "record"   // Collection + ID (+ Owner) — gamertags/teams/notifications/rosters/team_membership_requests
	ResISO       ResourceKind = "iso"      // ID = isos.id
	ResLANFile   ResourceKind = "lan_file" // ID = "<kind>/<id>", Owner = owning users.id or ""
	ResToken     ResourceKind = "token"    // ID = kid
	ResGuild     ResourceKind = "guild"    // ID = guild snowflake
)

// Room is a parsed WebSocket room name (ParseRoom). Host rooms carry the
// instance and optional class; every other registered type is addressed by
// its type name alone.
type Room struct {
	Type     string // "host" | "admin" | "public" | other registered type name
	Instance string // host rooms only ("all" / "summary" for the aggregates)
	Class    string // host rooms only; "" = bare host:<inst>
	Raw      string // the string the client sent
}

// hostRoomType is Room.Type for every "host:*" room (rooms.HostRoomPrefix).
const hostRoomType = "host"

// hostAggregateInstances are the reserved host "instances" that address the
// cross-instance feeds instead of a runner (rooms.reservedInstanceNames).
var hostAggregateInstances = map[string]bool{"all": true, "summary": true}

// IsHost reports whether the room is any "host:*" room.
func (rm Room) IsHost() bool { return rm.Type == hostRoomType }

// IsHostAggregate reports whether the room is host:all or host:summary.
func (rm Room) IsHostAggregate() bool {
	return rm.IsHost() && hostAggregateInstances[rm.Instance]
}

// Selector encodes the room the way scope selectors address it:
// "host:<inst>" (bare), "host:<inst>:<class>", "host:all", "host:summary",
// "admin", "public", or the raw type name for other registered rooms. The
// bare host room is intentionally two segments so "host:*:<class>" scopes
// never match it (A.10: bare room is pb_user/admin only).
func (rm Room) Selector() string {
	if !rm.IsHost() {
		return rm.Type
	}
	s := hostRoomType + ":" + rm.Instance
	if rm.Class != "" {
		s += ":" + rm.Class
	}
	return s
}

// Resource is what an action is applied to.
type Resource struct {
	Kind       ResourceKind
	ID         string
	Collection string            // ResRecord only
	Owner      string            // ResRecord / ResLANFile: owning users.id when known
	Room       Room              // ResRoom only
	Extra      map[string]string // "target_user" (role), "kind" (lan_file, token.mint), "gamertag" (lan_file)
}

// Global is the resource of selectorless actions.
func Global() Resource { return Resource{Kind: ResGlobal} }

// Instance addresses a scraper runner / container by name.
func Instance(name string) Resource { return Resource{Kind: ResInstance, ID: name} }

// Container addresses a container by name (box actions).
func Container(name string) Resource { return Resource{Kind: ResContainer, ID: name} }

// RoomRes wraps a parsed Room.
func RoomRes(r Room) Resource { return Resource{Kind: ResRoom, Room: r} }

// User addresses a users record (moderation).
func User(id string) Resource { return Resource{Kind: ResUser, ID: id} }

// Role addresses a role by slug for grant/revoke on targetUserID.
func Role(slug, targetUserID string) Resource {
	return Resource{Kind: ResRole, ID: slug, Extra: map[string]string{"target_user": targetUserID}}
}

// Team addresses a teams record.
func Team(id string) Resource { return Resource{Kind: ResTeam, ID: id} }

// Record addresses any record by collection + id with its owning user.
func Record(collection, id, ownerUserID string) Resource {
	return Resource{Kind: ResRecord, Collection: collection, ID: id, Owner: ownerUserID}
}

// ISO addresses an isos record.
func ISO(id string) Resource { return Resource{Kind: ResISO, ID: id} }

// LANFile addresses a served LAN save artifact ("<kind>/<id>"); the file
// kind is kept in Extra["kind"] for the station_file predicate and the owning
// gamertag (sanitized) is added by the caller as Extra["gamertag"] when the
// kind is a profile.
func LANFile(kind, id, ownerUserID string) Resource {
	return Resource{
		Kind:  ResLANFile,
		ID:    kind + "/" + id,
		Owner: ownerUserID,
		Extra: map[string]string{"kind": kind},
	}
}

// Token addresses an api_tokens row by kid.
func Token(kid string) Resource { return Resource{Kind: ResToken, ID: kid} }

// Guild addresses a Discord guild by snowflake.
func Guild(id string) Resource { return Resource{Kind: ResGuild, ID: id} }

// Selector is the string a scope selector is matched against (§3.2):
// ResGlobal → ""; ResRoom → Room.Selector(); everything else → ID.
func (r Resource) Selector() string {
	switch r.Kind {
	case ResGlobal:
		return ""
	case ResRoom:
		return r.Room.Selector()
	default:
		return r.ID
	}
}

// instance returns the instance an "S ∧ P:bound" check compares the
// principal's binding against: the room's instance for rooms, the ID for
// instances / containers, "" for everything else.
func (r Resource) instance() string {
	switch r.Kind {
	case ResRoom:
		if r.Room.IsHostAggregate() {
			return ""
		}
		return r.Room.Instance
	case ResInstance, ResContainer:
		return r.ID
	default:
		return ""
	}
}
