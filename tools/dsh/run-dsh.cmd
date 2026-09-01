@echo off
setlocal
node "%~dp0node_modules\@deepseek-ai\dsh\lib\bin.js" %*
exit /b %errorlevel%
