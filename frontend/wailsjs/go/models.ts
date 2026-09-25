export namespace app {
	
	export class AboutInfo {
	    appName: string;
	    appVersion: string;
	    appAuthor: string;
	    appURL: string;
	    bilibiliURL: string;
	    appDesc: string;
	    sponsorURL: string;
	
	    static createFrom(source: any = {}) {
	        return new AboutInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.appName = source["appName"];
	        this.appVersion = source["appVersion"];
	        this.appAuthor = source["appAuthor"];
	        this.appURL = source["appURL"];
	        this.bilibiliURL = source["bilibiliURL"];
	        this.appDesc = source["appDesc"];
	        this.sponsorURL = source["sponsorURL"];
	    }
	}
	export class ConfigInfo {
	    community: string;
	    ip: string;
	    key_masked: string;
	    key_set: boolean;
	    supernode: string;
	
	    static createFrom(source: any = {}) {
	        return new ConfigInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.community = source["community"];
	        this.ip = source["ip"];
	        this.key_masked = source["key_masked"];
	        this.key_set = source["key_set"];
	        this.supernode = source["supernode"];
	    }
	}
	export class ConnectionDetails {
	    connected: boolean;
	    virtualIP: string;
	    nodeName: string;
	    status: string;
	    sponsorURL: string;
	
	    static createFrom(source: any = {}) {
	        return new ConnectionDetails(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.connected = source["connected"];
	        this.virtualIP = source["virtualIP"];
	        this.nodeName = source["nodeName"];
	        this.status = source["status"];
	        this.sponsorURL = source["sponsorURL"];
	    }
	}
	export class ExpressError {
	    code: string;
	    message: string;
	    retryable: boolean;
	    action?: string;
	
	    static createFrom(source: any = {}) {
	        return new ExpressError(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.message = source["message"];
	        this.retryable = source["retryable"];
	        this.action = source["action"];
	    }
	}
	export class ExpressPeer {
	    id: string;
	    name: string;
	    netbirdIp: string;
	    connected: boolean;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new ExpressPeer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.netbirdIp = source["netbirdIp"];
	        this.connected = source["connected"];
	        this.path = source["path"];
	    }
	}
	export class ExpressService {
	    installed: boolean;
	    running: boolean;
	    version: string;
	    expectedVersion: string;
	    repairRequired: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ExpressService(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.installed = source["installed"];
	        this.running = source["running"];
	        this.version = source["version"];
	        this.expectedVersion = source["expectedVersion"];
	        this.repairRequired = source["repairRequired"];
	    }
	}
	export class ExpressState {
	    state: string;
	    roomId: string;
	    roomCodeMasked: string;
	    localIp: string;
	    connectedPath: string;
	    peers: ExpressPeer[];
	    peersStale: boolean;
	    service: ExpressService;
	    error?: ExpressError;
	    busyCommand: string;
	    hasSavedRoom: boolean;
	    disconnected: boolean;
	    roomCode: string;
	    relayEnabled: boolean;
	    relayBlocked: boolean;
	    isOwner: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ExpressState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.roomId = source["roomId"];
	        this.roomCodeMasked = source["roomCodeMasked"];
	        this.localIp = source["localIp"];
	        this.connectedPath = source["connectedPath"];
	        this.peers = this.convertValues(source["peers"], ExpressPeer);
	        this.peersStale = source["peersStale"];
	        this.service = this.convertValues(source["service"], ExpressService);
	        this.error = this.convertValues(source["error"], ExpressError);
	        this.busyCommand = source["busyCommand"];
	        this.hasSavedRoom = source["hasSavedRoom"];
	        this.disconnected = source["disconnected"];
	        this.roomCode = source["roomCode"];
	        this.relayEnabled = source["relayEnabled"];
	        this.relayBlocked = source["relayBlocked"];
	        this.isOwner = source["isOwner"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ModeInfo {
	    current: string;
	    nickname: string;
	    roomApiUrl: string;
	
	    static createFrom(source: any = {}) {
	        return new ModeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.current = source["current"];
	        this.nickname = source["nickname"];
	        this.roomApiUrl = source["roomApiUrl"];
	    }
	}
	export class NodeInfo {
	    name: string;
	    address: string;
	
	    static createFrom(source: any = {}) {
	        return new NodeInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.address = source["address"];
	    }
	}
	export class NodeLatencyInfo {
	    name: string;
	    address: string;
	    latency: number;
	
	    static createFrom(source: any = {}) {
	        return new NodeLatencyInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.address = source["address"];
	        this.latency = source["latency"];
	    }
	}

}

export namespace natdetect {
	
	export class NATResult {
	    type: string;
	    localIp: string;
	    localPort: number;
	    publicIp: string;
	    publicPort: number;
	    stunServerA: string;
	    stunServerB: string;
	    suggestion: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new NATResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.localIp = source["localIp"];
	        this.localPort = source["localPort"];
	        this.publicIp = source["publicIp"];
	        this.publicPort = source["publicPort"];
	        this.stunServerA = source["stunServerA"];
	        this.stunServerB = source["stunServerB"];
	        this.suggestion = source["suggestion"];
	        this.error = source["error"];
	    }
	}

}

export namespace updater {
	
	export class UpdateInfo {
	    hasUpdate: boolean;
	    currentVersion: string;
	    latestVersion: string;
	    downloadUrl?: string;
	    sha256?: string;
	    size?: number;
	    releaseNotes?: string;
	    minVersion?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hasUpdate = source["hasUpdate"];
	        this.currentVersion = source["currentVersion"];
	        this.latestVersion = source["latestVersion"];
	        this.downloadUrl = source["downloadUrl"];
	        this.sha256 = source["sha256"];
	        this.size = source["size"];
	        this.releaseNotes = source["releaseNotes"];
	        this.minVersion = source["minVersion"];
	        this.error = source["error"];
	    }
	}

}

