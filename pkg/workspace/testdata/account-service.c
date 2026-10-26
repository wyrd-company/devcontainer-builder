/*
 * A controlled libc account lookup for tests: getpwnam and getpwnam_r find
 * the account fixtureacct, with the home folder that FIXTURE_ACCOUNT_HOME
 * names, as an account service other than /etc/passwd would. Every other
 * name goes to the next lookup. LD_PRELOAD loads it into a dynamically
 * linked process; a static process does not load it.
 */
#define _GNU_SOURCE
#include <dlfcn.h>
#include <errno.h>
#include <pwd.h>
#include <stdlib.h>
#include <string.h>

static const char fixture[] = "fixtureacct";

struct passwd *getpwnam(const char *name) {
	if (strcmp(name, fixture) == 0) {
		static struct passwd entry;
		entry.pw_name = (char *)fixture;
		entry.pw_passwd = "x";
		entry.pw_uid = 30001;
		entry.pw_gid = 30001;
		entry.pw_gecos = "Fixture account";
		entry.pw_dir = getenv("FIXTURE_ACCOUNT_HOME");
		entry.pw_shell = "/bin/sh";
		return &entry;
	}
	struct passwd *(*next)(const char *) = dlsym(RTLD_NEXT, "getpwnam");
	return next(name);
}

int getpwnam_r(const char *name, struct passwd *pwd, char *buf, size_t buflen, struct passwd **result) {
	if (strcmp(name, fixture) == 0) {
		const char *home = getenv("FIXTURE_ACCOUNT_HOME");
		size_t need = strlen(home) + 1;
		if (need > buflen) {
			*result = NULL;
			return ERANGE;
		}
		memcpy(buf, home, need);
		pwd->pw_name = (char *)fixture;
		pwd->pw_passwd = "x";
		pwd->pw_uid = 30001;
		pwd->pw_gid = 30001;
		pwd->pw_gecos = "Fixture account";
		pwd->pw_dir = buf;
		pwd->pw_shell = "/bin/sh";
		*result = pwd;
		return 0;
	}
	int (*next)(const char *, struct passwd *, char *, size_t, struct passwd **) = dlsym(RTLD_NEXT, "getpwnam_r");
	return next(name, pwd, buf, buflen, result);
}
