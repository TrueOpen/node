package app

import (
	"fmt"
	"reflect"

	"cosmossdk.io/depinject"
)

// depinject.BindInterface takes the two type names as strings, and the name it
// compares against is depinject's own unexported fullyQualifiedTypeName:
//
//	fmt.Sprintf("%s/%v", pkgType.PkgPath(), typ)
//
// reflect.Type.String() is already "<shortpkg>.<Name>", so the short package
// name appears twice — "…/x/gov/types/types.BankKeeper", not
// "…/x/gov/types.BankKeeper". Every hand-written binding in this app got that
// wrong, and depinject does not report a binding that never matches: it falls
// back to the implicit single-implementation rule and silently handed x/gov,
// x/staking and both Hyperlane modules the raw bank keeper. The first vetoed
// proposal then reached bank.BaseKeeper.BurnCoins, which panics because the gov
// module account deliberately has no Burner permission, and a panic inside gov's
// EndBlocker halts the chain.
//
// bindGuardedInterface derives both names from the types themselves so the
// spelling cannot drift again. It panics during package initialisation — the
// loudest moment available — if the pair is not an interface and an
// implementation of it, because a binding depinject cannot use is exactly the
// failure mode this replaces.
func bindGuardedInterface[Iface any, Impl any]() depinject.Config {
	ifaceType := reflect.TypeOf((*Iface)(nil)).Elem()
	implType := reflect.TypeOf((*Impl)(nil)).Elem()
	if ifaceType.Kind() != reflect.Interface {
		panic(fmt.Sprintf("bindGuardedInterface: %v is not an interface", ifaceType))
	}
	if !implType.Implements(ifaceType) {
		panic(fmt.Sprintf("bindGuardedInterface: %v does not implement %v", implType, ifaceType))
	}
	return depinject.BindInterface(depinjectTypeName(ifaceType), depinjectTypeName(implType))
}

// depinjectTypeName mirrors depinject's fullyQualifiedTypeName
// (cosmossdk.io/depinject/container.go). It is unexported there, so this copy is
// pinned by TestDepinjectTypeNameMatchesUpstream, which binds a local interface
// to a local implementation through a real container and requires the injected
// value to be the implementation.
func depinjectTypeName(typ reflect.Type) string {
	pkgType := typ
	switch typ.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Array:
		pkgType = typ.Elem()
	}
	pkgPath := pkgType.PkgPath()
	if pkgPath == "" {
		return fmt.Sprintf("%v", typ)
	}
	return fmt.Sprintf("%s/%v", pkgPath, typ)
}
