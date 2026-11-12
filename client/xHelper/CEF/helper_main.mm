// CEF 子进程入口：Helper.app 里的可执行文件就是这份源码编出来的。
//
// 为什么单独放这里（而不是 xHelperMac/）：Helper 自带 main()，同一个 target 里再有一个
// main（App.swift 的 @main）会撞符号；这份由 scripts/fetch-cef.sh 用 clang++ 单独编译，
// 不经过 Xcode。渲染进程的注入（四层伪装第 3/4 层）之后也从这里挂 CefApp。

#include "include/cef_app.h"
#include "include/wrapper/cef_library_loader.h"

int main(int argc, char* argv[]) {
  // 框架是 dlopen 进来的，不走链接期的 @rpath（CEF mac 官方示例同款）。
  CefScopedLibraryLoader library_loader;
  if (!library_loader.LoadInHelper()) {
    return 1;
  }

  CefMainArgs main_args(argc, argv);
  return CefExecuteProcess(main_args, nullptr, nullptr);
}
