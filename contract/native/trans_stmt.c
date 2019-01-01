/**
 * @file    trans_stmt.c
 * @copyright defined in aergo/LICENSE.txt
 */

#include "common.h"

#include "ir_bb.h"
#include "ir_fn.h"
#include "trans_id.h"
#include "trans_blk.h"
#include "trans_exp.h"

#include "trans_stmt.h"

static void
stmt_trans_exp(trans_t *trans, ast_stmt_t *stmt)
{
    exp_trans_to_lval(trans, stmt->u_exp.exp);
    trans->is_lval = false;

    if (is_call_exp(stmt->u_exp.exp)) {
        bb_add_stmt(trans->bb, stmt);
    }
    else if (has_piggyback(trans->bb)) {
        int i;
        array_t *pgbacks = &trans->bb->pgbacks;

        for (i = 0; i < array_size(pgbacks); i++) {
            bb_add_stmt(trans->bb, array_get_stmt(pgbacks, i));
        }

        array_reset(pgbacks);
    }
}

static void
stmt_trans_assign(trans_t *trans, ast_stmt_t *stmt)
{
    ast_exp_t *l_exp = stmt->u_assign.l_exp;
    ast_exp_t *r_exp = stmt->u_assign.r_exp;

    /* TODO: When assigning to a struct variable, 
     * we must replace it with an assignment for each field */ 

    exp_trans_to_lval(trans, l_exp);
    exp_trans_to_rval(trans, r_exp);

    if (is_tuple_exp(l_exp)) {
        array_t *var_exps = l_exp->u_tup.exps;
        array_t *val_exps = r_exp->u_tup.exps;

        ASSERT1(is_tuple_exp(r_exp), r_exp->kind);

        if (array_size(var_exps) == array_size(val_exps)) {
            int i;
            ast_exp_t *var_exp, *val_exp;

            for (i = 0; i < array_size(val_exps); i++) {
                var_exp = array_get_exp(var_exps, i);
                val_exp = array_get_exp(val_exps, i);

                ASSERT(meta_cmp(&var_exp->meta, &val_exp->meta) == 0);

                bb_add_stmt(trans->bb, stmt_new_assign(var_exp, val_exp, &stmt->pos));
            }
        }
        else {
            int i, j;
            int var_idx = 0;
            ast_exp_t *var_exp;

            ASSERT2(array_size(var_exps) > array_size(val_exps),
                    array_size(var_exps), array_size(val_exps));

            for (i = 0; i < array_size(val_exps); i++) {
                ast_exp_t *val_exp = array_get_exp(val_exps, i);
                meta_t *val_meta = &val_exp->meta;

                if (is_tuple_type(val_meta)) {
                    for (j = 0; j < val_meta->elem_cnt; j++) {
                        ast_exp_t *var_exp = array_get_exp(var_exps, var_idx++);
                        ast_exp_t *elem_exp = array_get_exp(val_exp->u_tup.exps, j);

                        ASSERT(meta_cmp(&var_exp->meta, &elem_exp->meta) == 0);

                        bb_add_stmt(trans->bb,
                                    stmt_new_assign(var_exp, elem_exp, &stmt->pos));
                    }
                }
                else {
                    var_exp = array_get_exp(var_exps, var_idx++);
                    ASSERT(meta_cmp(&var_exp->meta, &val_exp->meta) == 0);

                    bb_add_stmt(trans->bb,
                                stmt_new_assign(array_get_exp(var_exps, var_idx++),
                                                val_exp, &stmt->pos));
                }
            }
        }
    }
    else {
        bb_add_stmt(trans->bb, stmt);
    }
}

static void
stmt_trans_if(trans_t *trans, ast_stmt_t *stmt)
{
    int i;
    ir_bb_t *prev_bb = trans->bb;
    ir_bb_t *next_bb = bb_new();
    array_t *elif_stmts = &stmt->u_if.elif_stmts;

    /* if statements were transformed like this:
     *
     *         .---------------------------.
     *         |         prev_bb           |
     *         '---------------------------'
     *         /           / \              \
     *  .------. .---------. .---------.     .------.
     *  |  if  | | else if | | else if | ... | else |
     *  '------' '---------' '---------'     '------'
     *         \           \ /              /
     *         .---------------------------.
     *         |         next_bb           |
     *         '---------------------------'
     */

    fn_add_basic_blk(trans->fn, prev_bb);

    trans->bb = bb_new();
    bb_add_branch(prev_bb, stmt->u_if.cond_exp, trans->bb);

    exp_trans(trans, stmt->u_if.cond_exp);

    if (stmt->u_if.if_blk != NULL)
        blk_trans(trans, stmt->u_if.if_blk);

    bb_add_branch(trans->bb, NULL, next_bb);

    fn_add_basic_blk(trans->fn, trans->bb);

    for (i = 0; i < array_size(elif_stmts); i++) {
        ast_stmt_t *elif_stmt = array_get_stmt(elif_stmts, i);

        trans->bb = bb_new();
        bb_add_branch(prev_bb, elif_stmt->u_if.cond_exp, trans->bb);

        exp_trans(trans, elif_stmt->u_if.cond_exp);

        if (elif_stmt->u_if.if_blk != NULL)
            blk_trans(trans, elif_stmt->u_if.if_blk);

        bb_add_branch(trans->bb, NULL, next_bb);

        fn_add_basic_blk(trans->fn, trans->bb);
    }

    if (stmt->u_if.else_blk != NULL) {
        trans->bb = bb_new();
        bb_add_branch(prev_bb, NULL, trans->bb);

        blk_trans(trans, stmt->u_if.else_blk);

        bb_add_branch(trans->bb, NULL, next_bb);

        fn_add_basic_blk(trans->fn, trans->bb);
    }
    else {
        bb_add_branch(prev_bb, NULL, next_bb);
    }

    trans->bb = next_bb;
}

static void
stmt_trans_for_loop(trans_t *trans, ast_stmt_t *stmt)
{
    ir_bb_t *prev_bb = trans->bb;
    ir_bb_t *cond_bb = bb_new();
    ir_bb_t *next_bb = bb_new();

    /* for-loop statements were transformed like this:
     *
     *         .---------------------.
     *         | prev_bb + init_stmt |
     *         '---------------------'
     *                    |
     *              .-----------.
     *              |  cond_bb  |<---------.
     *              '-----------'          |
     *                  /   \              |
     *       .-----------. .------------.  |
     *       |  next_bb  | |  loop blk  |--'
     *       '-----------' '------------'
     */

    if (stmt->u_loop.init_stmt != NULL)
        stmt_trans(trans, stmt->u_loop.init_stmt);

    /* previous basic block */
    bb_add_branch(prev_bb, NULL, cond_bb);

    fn_add_basic_blk(trans->fn, prev_bb);

    trans->bb = cond_bb;

    trans->cont_bb = cond_bb;
    trans->break_bb = next_bb;

    blk_trans(trans, stmt->u_loop.blk);

    trans->cont_bb = NULL;
    trans->break_bb = NULL;

    if (trans->bb != NULL) {
        /* make loop using last block and entry block */
        bb_add_branch(trans->bb, NULL, cond_bb);

        fn_add_basic_blk(trans->fn, trans->bb);
    }
    else {
        /* make loop using self block in case of an empty loop without loop_exp */
        bb_add_branch(cond_bb, NULL, cond_bb);
    }

    trans->bb = next_bb;
}

static void
stmt_trans_array_loop(trans_t *trans, ast_stmt_t *stmt)
{
    ERROR(ERROR_NOT_SUPPORTED, &stmt->pos);
}

static void
stmt_trans_loop(trans_t *trans, ast_stmt_t *stmt)
{
    switch (stmt->u_loop.kind) {
    case LOOP_FOR:
        stmt_trans_for_loop(trans, stmt);
        break;

    case LOOP_ARRAY:
        stmt_trans_array_loop(trans, stmt);
        break;

    default:
        ASSERT1(!"invalid loop", stmt->u_loop.kind);
    }
}

static void
stmt_trans_switch(trans_t *trans, ast_stmt_t *stmt)
{
    int i, j;
    ast_blk_t *blk = stmt->u_sw.blk;
    ir_bb_t *prev_bb = trans->bb;
    ir_bb_t *next_bb = bb_new();

    /* switch-case statements were transformed like this:
     *
     *         .---------------------------.
     *         |         prev_bb           |
     *         '---------------------------'
     *            /          |           \
     *    .----------. .----------.     .---------.
     *    |  case 1  | |  case 2  | ... | default |
     *    '----------' '----------'     '---------'
     *            \          |           /
     *         .---------------------------.
     *         |         next_bb           |
     *         '---------------------------'
     */

    fn_add_basic_blk(trans->fn, prev_bb);

    trans->cont_bb = NULL;
    trans->break_bb = next_bb;

    trans->bb = bb_new();

    for (i = 0; i < array_size(&blk->stmts); i++) {
        ast_stmt_t *case_stmt = array_get_stmt(&blk->stmts, i);

        bb_add_branch(prev_bb, case_stmt->u_case.val_exp, trans->bb);

        if (case_stmt->u_case.val_exp != NULL)
            /* default can be NULL */
            exp_trans(trans, case_stmt->u_case.val_exp);

        for (j = 0; j < array_size(case_stmt->u_case.stmts); j++) {
            stmt_trans(trans, array_get_stmt(case_stmt->u_case.stmts, j));
        }

        if (trans->bb != NULL) {
            /* There is no break statement */
            if (i == array_size(&blk->stmts) - 1) {
                bb_add_branch(trans->bb, NULL, next_bb);
                fn_add_basic_blk(trans->fn, trans->bb);
            }
            else {
                ir_bb_t *case_bb = bb_new();

                bb_add_branch(trans->bb, NULL, case_bb);
                fn_add_basic_blk(trans->fn, trans->bb);

                trans->bb = case_bb;
            }
        }
        else if (i < array_size(&blk->stmts) - 1) {
            trans->bb = bb_new();
        }
    }

    if (!stmt->u_sw.has_dflt)
        bb_add_branch(prev_bb, NULL, next_bb);

    trans->break_bb = NULL;
    trans->bb = next_bb;
}

static void
stmt_trans_return(trans_t *trans, ast_stmt_t *stmt)
{
    if (stmt->u_ret.arg_exp != NULL)
        exp_trans(trans, stmt->u_ret.arg_exp);

    bb_add_stmt(trans->bb, stmt);

    bb_add_branch(trans->bb, NULL, trans->fn->exit_bb);
    fn_add_basic_blk(trans->fn, trans->bb);

    trans->bb = NULL;
}

static void
stmt_trans_continue(trans_t *trans, ast_stmt_t *stmt)
{
    ASSERT(trans->cont_bb != NULL);

    bb_add_branch(trans->bb, NULL, trans->cont_bb);
    fn_add_basic_blk(trans->fn, trans->bb);

    trans->bb = NULL;
}

static void
stmt_trans_break(trans_t *trans, ast_stmt_t *stmt)
{
    ir_bb_t *next_bb = bb_new();

    ASSERT(trans->break_bb != NULL);

    if (stmt->u_jump.cond_exp != NULL) {
        exp_trans(trans, stmt->u_jump.cond_exp);

        bb_add_branch(trans->bb, stmt->u_jump.cond_exp, trans->break_bb);
        bb_add_branch(trans->bb, NULL, next_bb);
    }
    else {
        bb_add_branch(trans->bb, NULL, trans->break_bb);
    }

    fn_add_basic_blk(trans->fn, trans->bb);

    trans->bb = next_bb;
}

static void
stmt_trans_goto(trans_t *trans, ast_stmt_t *stmt)
{
    ast_id_t *jump_id = stmt->u_goto.jump_id;

    ASSERT(jump_id->u_lab.stmt->label_bb != NULL);

    bb_add_branch(trans->bb, NULL, jump_id->u_lab.stmt->label_bb);
    fn_add_basic_blk(trans->fn, trans->bb);

    trans->bb = NULL;
}

static void
stmt_trans_ddl(trans_t *trans, ast_stmt_t *stmt)
{
    bb_add_stmt(trans->bb, stmt);
}

static void
stmt_trans_blk(trans_t *trans, ast_stmt_t *stmt)
{
    if (stmt->u_blk.blk != NULL)
        blk_trans(trans, stmt->u_blk.blk);
}

void
stmt_trans(trans_t *trans, ast_stmt_t *stmt)
{
    if (stmt->label_bb != NULL) {
        if (trans->bb != NULL) {
            bb_add_branch(trans->bb, NULL, stmt->label_bb);
            fn_add_basic_blk(trans->fn, trans->bb);
        }

        trans->bb = stmt->label_bb;
    }
    else if (trans->bb == NULL) {
        trans->bb = bb_new();
    }

    switch (stmt->kind) {
    case STMT_NULL:
        break;

    case STMT_EXP:
        stmt_trans_exp(trans, stmt);
        break;

    case STMT_ASSIGN:
        stmt_trans_assign(trans, stmt);
        break;

    case STMT_IF:
        stmt_trans_if(trans, stmt);
        break;

    case STMT_LOOP:
        stmt_trans_loop(trans, stmt);
        break;

    case STMT_SWITCH:
        stmt_trans_switch(trans, stmt);
        break;

    case STMT_CASE:
        break;

    case STMT_RETURN:
        stmt_trans_return(trans, stmt);
        break;

    case STMT_CONTINUE:
        stmt_trans_continue(trans, stmt);
        break;

    case STMT_BREAK:
        stmt_trans_break(trans, stmt);
        break;

    case STMT_GOTO:
        stmt_trans_goto(trans, stmt);
        break;

    case STMT_DDL:
        stmt_trans_ddl(trans, stmt);
        break;

    case STMT_BLK:
        stmt_trans_blk(trans, stmt);
        break;

    default:
        ASSERT1(!"invalid statement", stmt->kind);
    }
}

/* end of trans_stmt.c */