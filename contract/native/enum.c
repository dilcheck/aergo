/**
 * @file    enum.c
 * @copyright defined in aergo/LICENSE.txt
 */

#include "common.h"

#include "gmp.h"

#include "enum.h"

char *type_names_[TYPE_MAX] = {
    "undefined",
    "bool",
    "byte",
    "int8",
    "int16",
    "int32",
    "int64",
    "int128",
    "int256",
    "float",
    "double",
    "string",
    "account",
    "struct",
    "map",
    "object",
    "cursor",
    "void",
    "tuple"
};

#define I32             4
#define I64             8
#define F32             4
#define F64             8
#define ADDR            4

int type_sizes_[TYPE_MAX] = {
    0,                  /* TYPE_NONE */
    I32,                /* TYPE_BOOL */
    I32,                /* TYPE_BYTE */
    I32,                /* TYPE_INT8 */
    I32,                /* TYPE_INT16 */
    I32,                /* TYPE_INT32 */
    I64,                /* TYPE_INT64 */
    ADDR,               /* TYPE_INT128 */
    ADDR,               /* TYPE_INT256 */
    F32,                /* TYPE_FLOAT */
    F64,                /* TYPE_DOUBLE */
    ADDR,               /* TYPE_STRING */
    ADDR,               /* TYPE_ACCOUNT */
    ADDR,               /* TYPE_STRUCT */
    I64,                /* TYPE_MAP */
    ADDR,               /* TYPE_OBJECT */
    ADDR,               /* TYPE_CURSOR */
    0,                  /* TYPE_VOID */
    ADDR                /* TYPE_TUPLE */
};

int type_bytes_[TYPE_MAX] = {
    0,                  /* TYPE_NONE */
    sizeof(bool),
    sizeof(uint8_t),
    sizeof(int8_t),
    sizeof(int16_t),
    sizeof(int32_t),
    sizeof(int64_t),
    sizeof(uint32_t),
    sizeof(uint32_t),
    sizeof(float),
    sizeof(double),
    sizeof(int32_t),    /* TYPE_STRING */
    sizeof(int32_t),    /* TYPE_ACCOUNT */
    sizeof(int32_t),    /* TYPE_STRUCT */
    sizeof(int32_t),    /* TYPE_MAP */
    sizeof(int32_t),    /* TYPE_OBJECT */
    sizeof(int32_t),    /* TYPE_CURSOR */
    0,                  /* TYPE_VOID */
    0                   /* TYPE_TUPLE */
};

/* end of enum.c */
