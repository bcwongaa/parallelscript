/*
 * What the RegExp sources (regexp_object.c, regexp_string.c) share; not part of the
 * runtime's API (D-133, D-134).
 */
#ifndef PTS_REGEXP_INTERNAL_H
#define PTS_REGEXP_INTERNAL_H

#include "pts.h"

enum { PTS_LOCAL_CAPTURES = 32 }; /* captures of up to 15 groups fit on the stack */

/* Room for regex's captures: local (PTS_LOCAL_CAPTURES entries) if they fit, else collected memory. */
int64_t *pts_regexp_captures_for(const pts_regex *regex, int64_t *local);

/*
 * RegExpBuiltinExec's search: whether regexp matches subject from its lastIndex (0 unless
 * it has g or y), filling captures and updating lastIndex as JS does. *cursor is a known
 * position at or before lastIndex ({0, 0} will do); it moves to the match's end.
 */
bool pts_regexp_match(pts_regexp *regexp, pts_string *subject, int64_t *captures, pts_regex_cursor *cursor);

/* A match array of a match found from *start, a known position at or before it (which this doesn't move). */
pts_match_array *pts_match_array_at(const pts_regex *regex, pts_string *subject, const int64_t *captures, pts_regex_cursor start);

/* Whether value is a RegExp. */
bool pts_is_regexp(pts_value value);

#endif
