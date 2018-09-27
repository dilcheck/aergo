/**
 * @file    util.h
 * @copyright defined in aergo/LICENSE.txt
 */

#ifndef _UTIL_H
#define _UTIL_H

#include "common.h"

#ifndef _STRBUF_T
#define _STRBUF_T
typedef struct strbuf_s strbuf_t;
#endif /* ! _STRBUF_T */

#define MAX(x, y)           ((x) > (y) ? (x) : (y))
#define MIN(x, y)           ((x) > (y) ? (y) : (x))

#define STR_ARG(v)          ((v) == NULL ? "" : (v))
#define BOOL_ARG(v)         ((v) ? "true" : "false")

FILE *open_file(char *path, char *mode);
void close_file(FILE *fp);

char *strtrim(char *str);
void strset(char *buf, char ch, int size);

#endif /* ! _UTIL_H */
