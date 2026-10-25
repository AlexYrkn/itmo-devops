#define _GNU_SOURCE
#include <errno.h>
#include <seccomp.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/prctl.h>
#include <sys/syscall.h>
#include <sys/utsname.h>
#include <unistd.h>

static void show_cap_eff(void) {
    FILE *f = fopen("/proc/self/status", "r");
    char line[256];
    if (f == NULL) return;
    while (fgets(line, sizeof(line), f) != NULL) {
        if (strncmp(line, "CapEff:", 7) == 0) {
            fprintf(stderr, "security: %s", line);
            break;
        }
    }
    fclose(f);
}

int main(int argc, char **argv) {
    scmp_filter_ctx ctx;
    struct utsname uts;

    if (argc < 3 || strcmp(argv[1], "--") != 0) {
        fprintf(stderr, "usage: %s -- PROGRAM [ARG...]\n", argv[0]);
        return 64;
    }

    show_cap_eff();
    errno = 0;
    if (sethostname("should-not-work", 15) == -1 && errno == EPERM)
        fputs("security: capability check blocked sethostname with EPERM\n", stderr);
    else {
        fputs("security: capability self-test FAILED\n", stderr);
        return 1;
    }
    if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) < 0) {
        perror("PR_SET_NO_NEW_PRIVS");
        return 1;
    }
    ctx = seccomp_init(SCMP_ACT_ALLOW);
    if (ctx == NULL) {
        fputs("seccomp_init failed\n", stderr);
        return 1;
    }
    if (seccomp_rule_add(ctx, SCMP_ACT_ERRNO(EPERM), SCMP_SYS(uname), 0) < 0 ||
        seccomp_load(ctx) < 0) {
        fputs("seccomp rule/load failed\n", stderr);
        seccomp_release(ctx);
        return 1;
    }
    seccomp_release(ctx);

    errno = 0;
    if (syscall(SYS_uname, &uts) == -1 && errno == EPERM)
        fputs("security: seccomp blocked uname with EPERM\n", stderr);
    else {
        fputs("security: seccomp self-test FAILED\n", stderr);
        return 1;
    }

    execv(argv[2], &argv[2]);
    perror("execv");
    return 127;
}
