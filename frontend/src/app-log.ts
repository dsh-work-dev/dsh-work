import {LogService} from "../bindings/github.com/local/dsh-work/internal/desktopclient";
import {createFrontendLog} from "./log";

/** The workbench frontend's log, written into the UI process's ui.log. */
export const appLog = createFrontendLog({write: (entries) => LogService.Write(entries)});
